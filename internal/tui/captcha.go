package tui

import (
	"bufio"
	"crypto/rand"
	"fmt"
	"math/big"
	"os"
	"strings"

	"make-sync/internal/util"
)

// ConfirmWithCaptcha prompts the user with a short random token and requires
// the exact token to be typed to confirm a destructive (force) operation.
// Returns true if confirmed, false if cancelled/failed. The function will
// automatically succeed (no prompt) when running non-interactively (stdin
// not a TTY) or when env var MAKE_SYNC_FORCE_CAPTCHA is set to "false".
func ConfirmWithCaptcha(prompt string, attempts int) (bool, error) {
	// env override
	if v := strings.ToLower(strings.TrimSpace(os.Getenv("MAKE_SYNC_FORCE_CAPTCHA"))); v == "false" || v == "0" || v == "no" {
		util.Default.Println("ℹ️  MAKE_SYNC_FORCE_CAPTCHA=false detected — skipping captcha")
		return true, nil
	}

	// detect TTY
	if fi, _ := os.Stdin.Stat(); (fi.Mode() & os.ModeCharDevice) == 0 {
		util.Default.Println("ℹ️  Non-interactive stdin detected — skipping captcha")
		return true, nil
	}

	if attempts <= 0 {
		attempts = 3
	}

	token, err := genToken(6)
	if err != nil {
		return false, fmt.Errorf("failed to generate token: %v", err)
	}

	reader := bufio.NewReader(os.Stdin)
	for i := 0; i < attempts; i++ {
		util.Default.Printf("⚠️  %s\n", prompt)
		util.Default.Printf("Type the token to confirm [%s]: ", token)
		// read line
		line, err := reader.ReadString('\n')
		if err != nil {
			return false, fmt.Errorf("failed to read input: %v", err)
		}
		entry := strings.TrimSpace(line)
		if entry == token {
			util.Default.Println("✅ Confirmation accepted")
			return true, nil
		}
		util.Default.Printf("❌ Token mismatch (%d/%d).\n", i+1, attempts)
	}
	util.Default.Println("⚠️  Confirmation failed — aborting force operation")
	return false, nil
}

// genToken generates an uppercase alphanumeric token of given length.
func genToken(n int) (string, error) {
	const charset = "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	out := make([]byte, n)
	max := big.NewInt(int64(len(charset)))
	for i := 0; i < n; i++ {
		r, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", err
		}
		out[i] = charset[r.Int64()]
	}
	return string(out), nil
}
