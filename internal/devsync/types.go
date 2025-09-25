package devsync

import (
	"context"
	"os/exec"
	"runtime"
	"sync"
	"time"

	"make-sync/internal/config"
	"make-sync/internal/sshclient"
	"make-sync/internal/util"

	"github.com/asaskevich/EventBus"
	"golang.org/x/term"

	"github.com/rjeczalik/notify"
)

type remoteInitState struct {
	once sync.Once
	err  error
	// done indicates whether initialization ran (1) or not (0).
	done uint32
}

// EventType represents the type of file system event
type EventType int

const (
	EventCreate EventType = iota
	EventWrite
	EventRemove
	EventRename
)

// executeLocalCommandWithOutput executes a command locally and returns output
func (w *Watcher) executeLocalCommandWithOutput(cmd string) (string, error) {
	util.Default.Printf("🔧 Executing: %s\n", cmd)

	// Use platform-appropriate shell: on Windows use cmd /C, otherwise bash -c
	var command *exec.Cmd
	if runtime.GOOS == "windows" {
		command = exec.Command("cmd", "/C", cmd)
	} else {
		command = exec.Command("bash", "-c", cmd)
	}
	command.Dir = "." // Execute in current directory

	output, err := command.CombinedOutput()
	if err != nil {
		util.Default.Printf("❌ Command failed: %v\n", err)
		util.Default.Printf("Output: %s\n", string(output))
		return "", err
	}

	return string(output), nil
}

// FileEvent represents a file system event with metadata
type FileEvent struct {
	Path      string
	EventType EventType
	IsDir     bool
	OldPath   string // For rename events
	Timestamp time.Time
}

// Session represents a remote or local terminal session
type Session struct {
	ID      string // Unique ID
	Name    string // Display name
	Type    string // "remote" or "local"
	Status  string // "running", "background", or "closed"
	Command string // Command being run (if any)
	// goroutine removed - not used
	// PTY support
	isActive bool // Whether this session is currently active/displayed
	// I/O handling
	// old PTY and IO fields removed; PTY persistence is handled by PTYManager/Bridge
}

// Watcher handles file system watching
type Watcher struct {
	ready             chan struct{}
	readyOnce         sync.Once
	config            *config.Config
	watchPath         string // Absolute path being watched
	watchChan         chan notify.EventInfo
	done              chan bool
	eventChan         chan FileEvent
	lastEvents        map[string]FileEvent // For debouncing
	sshClient         *sshclient.SSHClient
	extendedIgnores   []string            // Cached extended ignore patterns
	ignoreFileModTime time.Time           // Last modification time of .sync_ignore file
	fileCache         *FileCache          // File metadata cache
	sessions          map[string]*Session // Active sessions
	sessionCounter    int                 // For generating unique IDs
	keyboardStop      chan bool           // Channel to stop keyboard input during sessions
	keyboardRestart   chan bool           // Channel to restart keyboard input after sessions
	keyboardStopped   chan struct{}       // Ack channel: keyboard handler signals when it has stopped
	eventBus          EventBus.Bus        // Event bus for inter-component communication
	commands          *CommandManager     // Command manager for all command operations

	// notify stop control - used to stop only the file-notify subsystem
	notifyStopOnce sync.Once
	notifyStopped  chan struct{}
	// protect access to notifyStopOnce and notifyStopped
	notifyMu sync.Mutex

	// printing is centralized via util.Default
	// PTY manager for persistent remote sessions (slots 3..9)
	ptyMgr *PTYManager
	// NOTE: remote initialization is tracked in package-level registry
	Slot *int
	// isLocal indicates the menu is currently showing local commands submenu
	isLocal bool
	// runtime control
	running   bool
	runningMu sync.Mutex

	// Context for coordinated shutdown
	ctx        context.Context
	cancelFunc context.CancelFunc
	shutdownMu sync.Mutex

	// Agent process tracking
	agentPID string // PID of remote agent process

	configMu sync.RWMutex // protect reading/writing w.config or other config-derived state

	// protects access to extendedIgnores and ignoreFileModTime
	ignoresMu sync.RWMutex

	// KeyboardEvents receives shortcut events from TUI (e.g. "reload","stats","deploy","alt3")
	KeyboardEvents chan string
	// TUIActive indicates Bubble Tea is running; when true legacy raw keyboard handler should pause
	TUIActive bool

	oldState *term.State

	firstOld *term.State
}

// RemoteAgentConfig represents the configuration sent to remote agent
type RemoteAgentConfig struct {
	Devsync struct {
		Ignores        []string `json:"ignores"`
		AgentWatchs    []string `json:"agent_watchs"`
		ManualTransfer []string `json:"manual_transfer"`
		WorkingDir     string   `json:"working_dir"`
	} `json:"devsync"`
}
