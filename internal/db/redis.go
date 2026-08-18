package db

import "github.com/redis/go-redis/v9"

// NewRedisClient returns a single client for the given URL, matching
// NewPool's shape: one connection helper for the caller to construct
// once and share, not one client per caller.
func NewRedisClient(url string) (*redis.Client, error) {
	opt, err := redis.ParseURL(url)
	if err != nil {
		return nil, err
	}
	return redis.NewClient(opt), nil
}
