package syncdata

import (
	"make-sync/internal/tui"
	"make-sync/internal/util"
	"strings"
)

// PostOperationAction represents the action chosen after a sync operation
type PostOperationAction int

const (
	RetryOperation PostOperationAction = iota
	BackToMainMenu
)

// ShowPostSafePullMenu shows menu after safe pull operation completes
func ShowPostSafePullMenu() PostOperationAction {
	for {
		postMenuItems := []string{
			"retry_last_pull :: Retry last pull",
			"back_to_menu :: Back to main menu",
		}

		postResult, err := tui.ShowMenuWithPrints(postMenuItems, "Safe Pull Completed - What's next?")
		if err != nil {
			util.Default.Printf("❌ Post-menu selection cancelled: %v\n", err)
			return BackToMainMenu
		}

		if postResult == "retry_last_pull" || strings.HasPrefix(postResult, "retry") {
			util.Default.Println("🔄 Retrying safe pull...")
			return RetryOperation
		} else {
			util.Default.Println("🔄 Returning to main menu...")
			return BackToMainMenu
		}
	}
}

// ShowPostSafePushMenu shows menu after safe push operation completes
func ShowPostSafePushMenu() PostOperationAction {
	for {
		postMenuItems := []string{
			"retry_last_push :: Retry last push",
			"back_to_menu :: Back to main menu",
		}

		postResult, err := tui.ShowMenuWithPrints(postMenuItems, "Safe Push Completed - What's next?")
		if err != nil {
			util.Default.Printf("❌ Post-menu selection cancelled: %v\n", err)
			return BackToMainMenu
		}

		if postResult == "retry_last_push" || strings.HasPrefix(postResult, "retry") {
			util.Default.Println("🔄 Retrying safe push...")
			return RetryOperation
		} else {
			util.Default.Println("🔄 Returning to main menu...")
			return BackToMainMenu
		}
	}
}
