package seeding

import "context"

// Seeder registers a locally-available torrent with an external seeding service
// for persistent internet seeding. Implementations must treat "already seeding"
// as success and be safe for concurrent use.
type Seeder interface {
	// Seed registers the torrent identified by infohash. contentDir is the
	// directory on disk where the torrent's content lives.
	Seed(ctx context.Context, infohash string, contentDir string) error
}
