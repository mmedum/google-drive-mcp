package gapi

import "fmt"

// maxPages bounds a walk to the end of a listing, so a token that never
// ends cannot loop.
const maxPages = 100

// walkPages reads a listing to its end, one page per call of next.
//
// Only a missing token ends it. Drive's list responses say the token
// "will be absent if the end of the list has been reached", so a token
// on an empty page still means more (D-16). No live step can make Drive
// send such a page.
// https://developers.google.com/workspace/drive/api/reference/rest/v3/permissions/list#response-body
func walkPages[T any](method string, next func(token string) (items []T, nextToken string, err error)) ([]T, error) {
	var out []T
	token := ""
	for range maxPages {
		items, nextToken, err := next(token)
		if err != nil {
			return nil, err
		}
		out = append(out, items...)
		if nextToken == "" {
			return out, nil
		}
		token = nextToken
	}
	return nil, fmt.Errorf("%w: %s still had a continuation token after %d pages", ErrUnexpected, method, maxPages)
}
