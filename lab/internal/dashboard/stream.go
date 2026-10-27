package dashboard

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"centavo/lab/internal/replay"
)

const (
	// streamInterval is how often a stream reads the round again; the
	// panel's charts move a second at a time.
	streamInterval = time.Second
	// streamKeepAlive keeps proxies from closing a quiet stream.
	streamKeepAlive = 15 * time.Second
	// maxStreamDuration ends a stream; the client reopens it and gets a
	// fresh snapshot, so nothing is lost.
	maxStreamDuration = 30 * time.Minute
	maxStreams        = 200
	// streamWriteTimeout bounds each write, so a stuck client is dropped
	// instead of holding a slot.
	streamWriteTimeout = 10 * time.Second
	eventStreamMedia   = "text/event-stream"
)

const (
	streamSnapshot = "snapshot"
	streamSeconds  = "seconds"
	streamEvent    = "event"
	streamAuditor  = "auditor"
	streamFinished = "finished"
)

type secondsUpdate struct {
	// Seconds start at the last second already sent, which may have grown.
	Seconds []replay.Second `json:"seconds"`
}

type streamSession struct {
	w          io.Writer
	controller *http.ResponseController
	follower   *follower
	live       LiveFunc

	hasSnapshot bool
	sentSeconds int
	sentEvents  int
	lastReading time.Time
	lastWrite   time.Time
}

func (p panelHandlers) events(w http.ResponseWriter, r *http.Request) {
	round := r.PathValue("round")
	if !roundDirPattern.MatchString(round) {
		p.writeError(w, r, errNotFound)

		return
	}

	dir := filepath.Join(p.config.RoundsDir, round)

	_, err := os.Stat(dir) //nolint:gosec // G703: round matched roundDirPattern, so dir stays in the rounds directory.
	if err != nil {
		p.writeError(w, r, errNotFound)

		return
	}

	select {
	case p.streams <- struct{}{}:
		defer func() { <-p.streams }()
	default:
		p.writeError(w, r, errTooManyStreams)

		return
	}

	w.Header().Set("Content-Type", eventStreamMedia)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)

	session := &streamSession{w: w, controller: http.NewResponseController(w), follower: newFollower(dir), live: p.config.Live}

	err = session.run(r, streamInterval)
	if err != nil {
		p.config.Logger.WarnContext(r.Context(), "streaming a round", "round", round, "error", err)
	}
}

func (s *streamSession) run(r *http.Request, interval time.Duration) error {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	endsAt := time.Now().Add(maxStreamDuration)

	for time.Now().Before(endsAt) {
		isFinished, err := s.tick()
		if err != nil {
			return err
		}

		if isFinished {
			return nil
		}

		select {
		case <-r.Context().Done():
			return nil
		case <-ticker.C:
		}
	}

	return nil
}

func (s *streamSession) tick() (bool, error) {
	current, err := s.follower.advance()
	if err != nil {
		return false, err
	}

	if current == nil {
		return false, s.keepAlive()
	}

	err = s.sendRound(current)
	if err != nil {
		return false, err
	}

	err = s.sendReading()
	if err != nil {
		return false, err
	}

	isFinished := current.Verdict != "" || current.Error != ""
	if !isFinished {
		return false, s.keepAlive()
	}

	final, err := replay.FromRound(s.follower.dir)
	if err != nil {
		// A round that ended in an error has no report; what was followed
		// is all there is.
		final = *current
	}

	return true, s.send(streamFinished, final)
}

func (s *streamSession) sendRound(current *replay.Replay) error {
	if !s.hasSnapshot {
		s.hasSnapshot = true
		s.sentSeconds, s.sentEvents = len(current.Seconds), len(current.Events)

		return s.send(streamSnapshot, current)
	}

	from := max(s.sentSeconds-1, 0)
	hasSeconds := len(current.Seconds) > from

	if hasSeconds {
		err := s.send(streamSeconds, secondsUpdate{Seconds: current.Seconds[from:]})
		if err != nil {
			return err
		}

		s.sentSeconds = len(current.Seconds)
	}

	for _, event := range current.Events[s.sentEvents:] {
		err := s.send(streamEvent, event)
		if err != nil {
			return err
		}
	}

	s.sentEvents = len(current.Events)

	return nil
}

func (s *streamSession) sendReading() error {
	if s.live == nil {
		return nil
	}

	reading, hasReading := s.live()

	isNew := hasReading && reading.At.After(s.lastReading)
	if !isNew {
		return nil
	}

	s.lastReading = reading.At

	return s.send(streamAuditor, reading)
}

func (s *streamSession) keepAlive() error {
	if time.Since(s.lastWrite) < streamKeepAlive {
		return nil
	}

	return s.write(": keep-alive\n\n")
}

func (s *streamSession) send(name string, data any) error {
	content, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("encoding the %s message: %w", name, err)
	}

	return s.write("event: " + name + "\ndata: " + string(content) + "\n\n")
}

// The write deadline moves with each message, since the server's own write
// timeout would end the stream.
func (s *streamSession) write(message string) error {
	err := s.controller.SetWriteDeadline(time.Now().Add(streamWriteTimeout))
	if err != nil && !errors.Is(err, http.ErrNotSupported) {
		return err
	}

	_, err = io.WriteString(s.w, message)
	if err != nil {
		return err
	}

	s.lastWrite = time.Now()

	return s.controller.Flush()
}
