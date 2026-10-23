package postgres

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

type sampleRow struct {
	ID        uuid.UUID `db:"id"`
	Status    string    `db:"status" read:"status::text"`
	CreatedAt time.Time `db:"created_at"`
}

func TestSelectFrom_WritesTheStatementFromTheRowModel(t *testing.T) {
	t.Parallel()

	query := selectFrom[sampleRow]("reading samples", "samples").
		Where("status = @status", args{"status": "created"}).
		Where("id = ANY(@ids)", args{"ids": []uuid.UUID{}}).
		OrderBy("created_at").
		Limit(10).
		ForUpdate()

	want := "SELECT id, status::text AS status, created_at FROM samples" +
		" WHERE status = @status AND id = ANY(@ids) ORDER BY created_at LIMIT @limit FOR UPDATE"
	if got := query.SQL(); got != want {
		t.Fatalf("SQL:\n got %s\nwant %s", got, want)
	}

	if len(query.args) != 3 || query.args["status"] != "created" || query.args["limit"] != 10 {
		t.Fatalf("args: %v", query.args)
	}
}

func TestInsertRow_BindsEveryColumnByName(t *testing.T) {
	t.Parallel()

	id := uuid.New()
	query := insertRow("inserting a sample", "samples", sampleRow{ID: id, Status: "created"}).Returning("created_at")

	want := "INSERT INTO samples (id, status, created_at) VALUES (@id, @status, @created_at) RETURNING created_at"
	if got := query.SQL(); got != want {
		t.Fatalf("SQL:\n got %s\nwant %s", got, want)
	}

	if query.args["id"] != id || query.args["status"] != "created" {
		t.Fatalf("args: %v", query.args)
	}
}

func TestUpdate_RefusesToRunWithoutACondition(t *testing.T) {
	t.Parallel()

	query := update("touching samples", "samples").Set("status", "failed").SetSQL("updated_at = now()", nil)

	if got, want := query.SQL(), "UPDATE samples SET status = @status, updated_at = now()"; got != want {
		t.Fatalf("SQL:\n got %s\nwant %s", got, want)
	}

	_, err := query.Exec(t.Context(), nil)
	if err == nil {
		t.Fatal("an update without Where ran")
	}
}

func TestQueries_DoNotShareArgumentsAcrossBranches(t *testing.T) {
	t.Parallel()

	base := selectFrom[sampleRow]("reading samples", "samples").Where("status = @status", args{"status": "created"})
	left := base.Where("id = @id", args{"id": uuid.New()})
	right := base.Where("created_at > @since", args{"since": time.Now()})

	_, leftHasSince := left.args["since"]
	_, rightHasID := right.args["id"]

	if leftHasSince || rightHasID || len(base.where) != 1 {
		t.Fatalf("branches leaked into each other: base %v, left %v, right %v", base.args, left.args, right.args)
	}
}

type embeddingRow struct {
	sampleRow

	Note string `db:"note"`
}

func TestColumnsOf_FlattensEmbeddedRowModels(t *testing.T) {
	t.Parallel()

	got := selectList[embeddingRow]()
	if want := "id, status::text AS status, created_at, note"; got != want {
		t.Fatalf("select list: got %s, want %s", got, want)
	}

	values := valuesOf(embeddingRow{sampleRow: sampleRow{Status: "sent"}, Note: "n"})
	if values["status"] != "sent" || values["note"] != "n" {
		t.Fatalf("values: %v", values)
	}
}
