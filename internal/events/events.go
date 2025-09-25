package events

import "github.com/asaskevich/EventBus"

// GlobalBus is the shared event bus for the entire application
var GlobalBus EventBus.Bus

func init() {
	GlobalBus = EventBus.New()
}

// Event types for application-wide coordination
const (
	// Shutdown events
	EventShutdownRequested = "app:shutdown:requested"
	EventShutdownComplete  = "app:shutdown:complete"

	// Watcher events
	EventWatcherStarted = "watcher:started"
	EventWatcherStopped = "watcher:stopped"

	// Cleanup events
	EventCleanupRequested = "app:cleanup:requested"

	// Session events
	EventSessionCompleted = "session:completed"
)
