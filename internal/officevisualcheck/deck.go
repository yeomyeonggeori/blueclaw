package officevisualcheck

import "context"

type Deck interface {
	Manifest(context.Context) (Manifest, error)
	Image(context.Context, string) ([]byte, error)
	Rebuild(context.Context, map[int]string) (Manifest, error)
}
