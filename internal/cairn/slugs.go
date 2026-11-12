package cairn

import (
	"crypto/rand"
	"database/sql"
	"fmt"
	"math/big"
	"strings"
)

// Pages keep their path when sharing changes. New paths default to three
// case-sensitive alphanumeric characters; owners can choose a longer default.
const slugAlphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

func randomSlugChars(length int) (string, error) {
	var path strings.Builder
	for j := 0; j < length; j++ {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(slugAlphabet))))
		if err != nil {
			return "", err
		}
		path.WriteByte(slugAlphabet[n.Int64()])
	}
	return path.String(), nil
}

// slugHolder returns the slug or public link name that a new slug would
// clash with, or "" when it is free. New slugs must differ from every
// existing one by more than letter case, so lamp-report and Lamp-Report
// never name two pages. Lookups stay exact, so pages saved before this rule
// that differ only in case keep working.
func (s *Server) slugHolder(slug string) (string, error) {
	var existing string
	err := s.db.QueryRow(`SELECT CASE WHEN lower(slug)=lower(?1) THEN slug ELSE public_slug END FROM pages WHERE lower(slug)=lower(?1) OR lower(public_slug)=lower(?1) ORDER BY slug=?1 DESC, public_slug=?1 DESC LIMIT 1`, slug).Scan(&existing)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return existing, err
}

func (s *Server) slugTaken(slug string) (bool, error) {
	existing, err := s.slugHolder(slug)
	return existing != "", err
}

// randomSlug generates a private convenience link using the owner setting.
// Kept under its historic name for existing library and API callers.
func (s *Server) randomSlug() (string, error) {
	length := s.preferences().LinkLength
	for i := 0; i < 32; i++ {
		slug, err := randomSlugChars(length)
		if err != nil {
			return "", err
		}
		if !validSlug(slug) {
			continue
		}
		taken, err := s.slugTaken(slug)
		if err != nil {
			return "", err
		}
		if !taken {
			return slug, nil
		}
	}
	return "", fmt.Errorf("could not create a link; try again")
}
