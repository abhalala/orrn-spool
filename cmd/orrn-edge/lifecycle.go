package main

// runLifecycleCommand dispatches native service lifecycle commands. Platform
// implementations return installer guidance where native SCM support is absent.
func runLifecycleCommand(subcmd string, args []string) (bool, error) {
	switch subcmd {
	case "install":
		return true, runInstallCommand(args)
	case "uninstall":
		return true, runUninstallCommand(args)
	case "upgrade":
		return true, runUpgradeCommand(args)
	default:
		return false, nil
	}
}
