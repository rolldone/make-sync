package config

import (
	"reflect"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestManualTransferDualFormatParsing(t *testing.T) {
	yamlText := `
project_name: demo
localPath: .
devsync:
  os_target: linux
  auth:
    username: tester
    privateKey: .ssh/id_rsa
    host: 127.0.0.1
    port: "22"
    remotePath: /tmp
  ignores: []
  agent_watchs: []
  manual_transfer:
    - vendor
    - path: assets
      ignores:
        - lib_a
        - kokoko.txt
        - !lib_a/ttt.txt
  script:
    local:
      on_ready: ""
      on_stop: ""
    remote:
      on_ready: ""
      on_stop: ""
  trigger_permission:
    unlink_folder: false
    unlink: false
    change: true
    add: true
`

	var cfg Config
	if err := yaml.Unmarshal([]byte(yamlText), &cfg); err != nil {
		t.Fatalf("yaml unmarshal failed: %v", err)
	}

	expectedPaths := []string{"vendor", "assets"}
	if !reflect.DeepEqual(cfg.Devsync.ManualTransfer, expectedPaths) {
		t.Fatalf("manual_transfer paths mismatch: got=%v want=%v", cfg.Devsync.ManualTransfer, expectedPaths)
	}

	rules, ok := cfg.Devsync.ManualTransferIgnores["assets"]
	if !ok {
		t.Fatalf("expected ignores for path 'assets'")
	}
	expectedRules := []string{"lib_a", "kokoko.txt", "!lib_a/ttt.txt"}
	if !reflect.DeepEqual(rules, expectedRules) {
		t.Fatalf("manual_transfer ignores mismatch: got=%v want=%v", rules, expectedRules)
	}

	if _, exists := cfg.Devsync.ManualTransferIgnores["vendor"]; exists {
		t.Fatalf("string-style manual_transfer entry should not create ignore profile")
	}
}

func TestManualTransferObjectRequiresPath(t *testing.T) {
	yamlText := `
project_name: demo
localPath: .
devsync:
  os_target: linux
  auth:
    username: tester
    privateKey: .ssh/id_rsa
    host: 127.0.0.1
    port: "22"
    remotePath: /tmp
  ignores: []
  agent_watchs: []
  manual_transfer:
    - path: ""
      ignores:
        - tmp
  script:
    local:
      on_ready: ""
      on_stop: ""
    remote:
      on_ready: ""
      on_stop: ""
  trigger_permission:
    unlink_folder: false
    unlink: false
    change: true
    add: true
`

	var cfg Config
	if err := yaml.Unmarshal([]byte(yamlText), &cfg); err == nil {
		t.Fatalf("expected unmarshal error for empty manual_transfer.path")
	}
}
