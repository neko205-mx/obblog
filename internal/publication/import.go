package publication

import (
	"crypto/rand"
	"encoding/hex"
	"strings"
	"time"

	"obblog/internal/vault"
)

// ImportNote identifies a just-written note without trusting its live revision.
type ImportNote struct {
	Path, Revision string
	PublishedAt    time.Time
}

// ImportExternalNotes publishes the whole batch in one atomic store transaction.
// Existing publication records (including withdrawn articles) are never edited.
func (s *Store) ImportExternalNotes(v *vault.Vault, notes []ImportNote) ([]*Article, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(notes) == 0 {
		return nil, nil
	}
	next := cloneMap(s.articles)
	created := make([]*Article, 0, len(notes))
	now := time.Now()
	for _, note := range notes {
		if !strings.HasPrefix(note.Path, "typecho/") || !vault.ValidPath(note.Path) || vault.Kind(note.Path) != "note" || next[note.Path] != nil || !vault.ValidRevision(note.Revision) || note.PublishedAt.IsZero() || note.PublishedAt.Year() < 1 || note.PublishedAt.After(now) {
			return nil, vault.ErrConflict
		}
		raw, _, err := v.ReadNote(note.Path)
		if err != nil {
			return nil, err
		}
		if vault.Revision(raw) != note.Revision {
			return nil, vault.ErrConflict
		}
		id := make([]byte, 16)
		if _, err := rand.Read(id); err != nil {
			return nil, err
		}
		article, err := s.copySource(v, nil, &Article{ID: hex.EncodeToString(id), Path: note.Path, PublishedAt: note.PublishedAt.UTC(), UpdatedAt: note.PublishedAt.UTC(), ExternalImport: true}, raw)
		if err != nil {
			return nil, err
		}
		next[note.Path] = article
		created = append(created, article)
	}
	// This operation adds files only. Keep the normal sync watermark so an
	// unrelated queued rename can still be discovered by the next refresh.
	if err := s.persist(next, s.blog, s.gitHead); err != nil {
		return nil, err
	}
	return created, nil
}
