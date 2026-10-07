package config

import "log/slog"

// RestrictFileToOwner limits access to a file that holds secrets (app
// secrets, API keys, tokens) to the current user.
//
// On Unix the callers create such files with mode 0600 and this is a no-op.
// On Windows the mode bits are ignored and a new file inherits its
// directory's ACL, which outside the user profile often grants Authenticated
// Users or Users access; the file gets a protected DACL granting full control
// to the current user, SYSTEM and Administrators only.
func RestrictFileToOwner(path string) error {
	return restrictFileToOwner(path)
}

// ProtectSecretFile applies RestrictFileToOwner and logs a warning when it
// fails: a config write must not fail because its ACL could not be tightened.
func ProtectSecretFile(path string) {
	if err := RestrictFileToOwner(path); err != nil {
		slog.Warn("config: could not restrict file access to the current user",
			"path", path, "error", err)
	}
}
