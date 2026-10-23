// The arena's sound cues, synthesized so the panel ships no audio files.
// Sound only repeats what the screen already shows.

export type Cue = 'attack' | 'pass' | 'fail';

type Tone = { readonly frequency: number; readonly seconds: number; readonly wave: OscillatorType };

const TONES: Readonly<Record<Cue, Tone>> = {
  attack: { frequency: 90, seconds: 0.18, wave: 'sine' },
  pass: { frequency: 880, seconds: 0.6, wave: 'triangle' },
  fail: { frequency: 110, seconds: 1.1, wave: 'sawtooth' },
};

const VOLUME = 0.18;
const SILENCE = 0.0001;

// One context for the page: browsers cap how many can exist.
let context: AudioContext | null = null;

// unlockAudio creates or resumes the context; browsers allow it only from a
// click, so the sound toggle calls it.
export function unlockAudio(): void {
  if (typeof AudioContext === 'undefined') {
    return;
  }

  context ??= new AudioContext();
  // A context that stays suspended only means silence.
  void context.resume().catch(() => undefined);
}

export function playCue(cue: Cue): void {
  if (context === null) {
    return;
  }

  const tone = TONES[cue];
  const oscillator = context.createOscillator();
  const gain = context.createGain();
  const start = context.currentTime;

  oscillator.type = tone.wave;
  oscillator.frequency.setValueAtTime(tone.frequency, start);
  gain.gain.setValueAtTime(VOLUME, start);
  gain.gain.exponentialRampToValueAtTime(SILENCE, start + tone.seconds);
  oscillator.connect(gain).connect(context.destination);
  oscillator.start(start);
  oscillator.stop(start + tone.seconds);
}
