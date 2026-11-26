package session

// SetBeforePromptWrite sets the hook that runs between promptSent and the
// write of session/prompt, and clears it when the test ends.
func SetBeforePromptWrite(t interface{ Cleanup(func()) }, f func(id string)) {
	beforePromptWrite = f
	t.Cleanup(func() { beforePromptWrite = nil })
}
