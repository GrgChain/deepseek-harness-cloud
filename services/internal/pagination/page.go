package pagination

import "errors"

// Offset supports direct page navigation alongside existing cursor clients.
// Page zero means the caller did not request numbered pagination.
func Offset(page, limit int, cursor string) (int64, error) {
	if page < 0 || page > 1_000_000_000 || limit < 1 || limit > 100 || (page > 0 && cursor != "") {
		return 0, errors.New("invalid page parameters")
	}
	if page == 0 {
		return 0, nil
	}
	return int64(page-1) * int64(limit), nil
}
