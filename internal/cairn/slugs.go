package cairn

import (
	"crypto/rand"
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

func (s *Server) slugTaken(slug string) (bool, error) {
	var count int
	if err := s.db.QueryRow(`SELECT count(*) FROM pages WHERE slug=? OR public_slug=?`, slug, slug).Scan(&count); err != nil {
		return false, err
	}
	return count != 0, nil
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
