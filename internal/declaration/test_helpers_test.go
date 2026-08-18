package declaration

import (
	"os"
	"time"

	"github.com/google/uuid"

	"portcoord/internal/apperr"
	"portcoord/internal/audit"
	"portcoord/internal/store"
)

func osStat(p string) (os.FileInfo, error) { return os.Stat(p) }

func auditNew(st *store.SQLiteStore, clock apperr.Clock) *audit.Recorder {
	return audit.New(st, clock)
}

// NewDeclaration creates a valid declaration submit request for testing.
func NewDeclaration(shipName string) SubmitRequest {
	return SubmitRequest{
		ShipName:       shipName,
		IMONumber:      "IMO" + shipName,
		VoyageNumber:   "V001",
		ETA:            time.Date(2026, 1, 2, 10, 0, 0, 0, time.UTC),
		CargoType:      "containers",
		CargoVolume:    10,
		CargoUnit:      "TEU",
		DeclaredBy:     "agent-1",
		DeclaringParty: "ship_owner",
		Priority:       5,
		IdempotencyKey: "idem-" + uuid.NewString(),
	}
}
