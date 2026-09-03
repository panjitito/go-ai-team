package server

// A list endpoint must always answer with a JSON array.
//
// Go encodes a nil slice as `null`, not `[]`. Several store filters return nil
// when nothing matches, so `GET /api/commands` on a project with no saved
// commands answered `null` — and the client, reasonably, did `.length` on it
// and crashed the whole view. The bug is not in the client: a list endpoint
// that sometimes returns a non-list is a broken contract.
//
// Every list handler goes through this, so the guarantee holds in one place
// rather than depending on each store method happening to preallocate.
func orEmpty[T any](in []T) []T {
	if in == nil {
		return []T{}
	}
	return in
}
