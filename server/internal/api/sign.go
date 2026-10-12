package api

import "github.com/Sakana-1314/MList/server/internal/vfs"

// signEntry fills in the preview signature for an entry. The digest -> file
// mapping is registered by vfs when the digest is computed (see onDigest), so
// this only needs the digest itself and never sees a real path.
func (a *API) signEntry(e *vfs.Entry) {
	if e.Thumb == "" {
		return
	}
	e.ThumbSig = a.Auth.ThumbSignature(e.Thumb)
}

func (a *API) signEntries(entries []vfs.Entry) {
	for i := range entries {
		a.signEntry(&entries[i])
	}
}
