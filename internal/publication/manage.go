package publication

import (
	"io/fs"
	"time"

	"obblog/internal/vault"
)

// SetPublishedBatch updates existing records in a single store transaction.
func (s *Store) SetPublishedBatch(v *vault.Vault, paths []string, enabled bool) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, path := range paths {
		if s.articles[path] == nil {
			return 0, fs.ErrNotExist
		}
	}
	next := cloneMap(s.articles)
	var snapshot *vault.Snapshot
	changed := 0
	for _, path := range paths {
		previous := next[path]
		if previous.Enabled == enabled {
			continue
		}
		article := *previous
		if enabled {
			var err error
			if snapshot == nil {
				snapshot, err = v.Scan()
				if err != nil {
					return 0, err
				}
			}
			if snapshot.Files[path] == nil {
				return 0, fs.ErrNotExist
			}
			copy, err := s.copy(v, snapshot, previous)
			if err != nil {
				return 0, err
			}
			article = *copy
		}
		article.Enabled = enabled
		next[path] = &article
		changed++
	}
	if changed == 0 {
		return 0, nil
	}
	if err := s.persist(next, s.blog, s.gitHead); err != nil {
		return 0, err
	}
	return changed, nil
}

// SetPublishedDate changes the date without altering availability or content.
func (s *Store) SetPublishedDate(path string, date time.Time) (*Article, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	previous := s.articles[path]
	if previous == nil {
		return nil, fs.ErrNotExist
	}
	article := *previous
	article.PublishedAt = date.UTC()
	article.UseFrontMatterDate = false
	next := cloneMap(s.articles)
	next[path] = &article
	if err := s.persist(next, s.blog, s.gitHead); err != nil {
		return nil, err
	}
	return &article, nil
}
