package main

import (
	"os"
	"testing"

	"github.com/Adelodunpeter25/sim-go/internal/idb"
)

// The live tests share pooled idb companions through the process-wide pool.
// A test binary that exits without closing it orphans those processes.
func TestMain(m *testing.M) {
	code := m.Run()
	idb.CloseAll()
	os.Exit(code)
}
