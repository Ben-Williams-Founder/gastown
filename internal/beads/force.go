package beads

// NeedsForceForID is retained for older callsites that build bd create args
// directly. Multi-hyphen IDs must route to their prefix owner instead of
// bypassing bd's prefix validation with --force.
func NeedsForceForID(id string) bool {
	return false
}
