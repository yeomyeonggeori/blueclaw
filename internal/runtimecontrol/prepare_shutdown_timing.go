package runtimecontrol

import "time"

// PrepareShutdownWindow is how long the host supervisor waits for a guest's
// POST /admin/api/runtime/prepare-shutdown response before it kills the guest
// regardless (cmd/blueclaw-supervisor/main.go).
const PrepareShutdownWindow = 10 * time.Second

// MemoryDrainDeadline bounds how long the prepare-shutdown handler spends
// draining the memory update queue, leaving room within PrepareShutdownWindow
// for task interruption bookkeeping and the response round trip back to the
// supervisor.
const MemoryDrainDeadline = PrepareShutdownWindow * 2 / 5
