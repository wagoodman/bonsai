package libz

// Z exists only to be referenced from a dead (never-called) function in app, so libz is a
// real source import that dead-code elimination removes from the binary. the e2e asserts it
// never shows up in the analysis, proving the live-set filter drops DCE-eliminated imports.
var marker = [...]string{"zulu", "yankee", "xray", "whiskey"}

//go:noinline
func Z() string {
	out := ""
	for _, s := range marker {
		out += s + "="
	}
	return out
}
