package service

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/mmedum/google-drive-mcp/v2/internal/gapi"
	"github.com/mmedum/google-drive-mcp/v2/internal/gdrive"
	"github.com/mmedum/google-drive-mcp/v2/internal/model"
)

// MaxUnderFolders is how many folders one search with under_folder
// covers, the named folder included. Drive cannot search a folder
// recursively, so every folder goes into the query as its own
// `'id' in parents` term, about 56 bytes once the URL encodes it. Google
// documents no limit on a query's length or its number of terms. A
// hundred folders is about 5.6 KB, which leaves a search's other fields
// under the 8 KB request line most HTTP servers accept. Where Drive
// itself refuses is unverified (§18); the live driver looks for it.
const MaxUnderFolders = 100

// folderSetTTL is how long the folders found under a folder are kept for
// the next page of the same search. A cached set only saves the walk: a
// continuation that misses it walks again and is refused if the set
// changed, so the time only decides what a second page costs.
const folderSetTTL = 5 * time.Minute

// underPagePrefix marks a page_token this server wrapped round Drive's,
// for a search with under_folder.
const underPagePrefix = "under."

// folderSet is a folder and every folder below it, as a search covers
// them.
type folderSet struct {
	root *gdrive.File
	// ids are the folders, the root included, sorted so the query they
	// make is the same however Drive ordered the walk.
	ids []string
	// unlistable names the folders this account can see but not list:
	// what is below them may be missing from the set.
	unlistable []string
	// digest identifies ids, so a continuation can tell the set it
	// recomputed is the one the first page searched.
	digest string
}

// clause is the query group that keeps a search inside the set.
func (f *folderSet) clause() string {
	terms := make([]string, 0, len(f.ids))
	for _, id := range f.ids {
		terms = append(terms, quote(id)+" in parents")
	}
	return "(" + strings.Join(terms, " or ") + ")"
}

// words describe the set in a search's title.
func (f *folderSet) words() string {
	return fmt.Sprintf("anywhere under %s (%s)", f.root.Name, model.Plural(len(f.ids), "folder", "folders"))
}

// note names the folders the walk could not list, or is empty.
func (f *folderSet) note() string {
	if len(f.unlistable) == 0 {
		return ""
	}
	return fmt.Sprintf("this account cannot list %s under %s: %s. Folders below them are not searched.",
		model.Plural(len(f.unlistable), "folder", "folders"), f.root.Name, strings.Join(f.unlistable, ", "))
}

// underPage is what a search with under_folder carries from one page to
// the next: which folder it searched, which set of folders, and Drive's
// own token.
type underPage struct {
	Folder string `json:"f"`
	Digest string `json:"s"`
	Token  string `json:"t"`
}

// wrap turns Drive's next-page token into one that carries the folder
// set it belongs to. Empty stays empty: there is no next page.
func (f *folderSet) wrap(token string) string {
	if token == "" {
		return ""
	}
	raw, _ := json.Marshal(underPage{Folder: f.root.ID, Digest: f.digest, Token: token})
	return underPagePrefix + base64.RawURLEncoding.EncodeToString(raw)
}

// underFolder resolves under_folder and the folders below it, and works
// out Drive's page token for this page. A page_token must come from a
// search under the same folder, over the same folders: Drive's token
// belongs to its query, and the query holds the set.
func (s *Service) underFolder(ctx context.Context, in *SearchInput) (*folderSet, string, error) {
	ref := strings.TrimSpace(in.UnderFolder)
	wrapped := strings.HasPrefix(in.PageToken, underPagePrefix)
	switch {
	case ref == "" && wrapped:
		return nil, "", Errorf(ClassInvalid, "this page_token came from a search with under_folder: pass the "+
			"same under_folder with it, or search again without page_token")
	case ref == "":
		return nil, in.PageToken, nil
	case strings.TrimSpace(in.InFolder) != "":
		return nil, "", Errorf(ClassInvalid, "pass in_folder for one folder's direct contents or under_folder "+
			"for everything below a folder, not both")
	case in.PageToken != "" && !wrapped:
		return nil, "", Errorf(ClassInvalid, "this page_token did not come from a search with under_folder, so "+
			"it cannot continue one. Search again without page_token")
	}
	var page underPage
	if wrapped {
		raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(in.PageToken, underPagePrefix))
		if err != nil || json.Unmarshal(raw, &page) != nil || page.Token == "" {
			return nil, "", Errorf(ClassInvalid, "this page_token is not one this server gave out. Search again "+
				"without page_token")
		}
	}
	res, err := s.Resolve(ctx, ref, ResolveOptions{FollowShortcut: true})
	if err != nil {
		return nil, "", err
	}
	root := res.File
	if !root.IsFolder() {
		return nil, "", Errorf(ClassInvalid, "under_folder must name a folder; %q is %s", ref, model.KindWithArticle(root))
	}
	if wrapped && page.Folder != root.ID {
		return nil, "", Errorf(ClassInvalid, "this page_token came from a search under another folder. Pass "+
			"the under_folder it came with, or search again without page_token")
	}
	if set, ok := s.cachedFolderSet(root.ID, page.Digest, in.Trashed); ok {
		return set, page.Token, nil
	}
	set, err := s.walkFolders(ctx, root, in.Trashed)
	if err != nil {
		return nil, "", err
	}
	if wrapped && set.digest != page.Digest {
		return nil, "", Errorf(ClassInvalid, "the folders under %s changed since the first page, so this "+
			"page would not continue it. Search again without page_token", root.Name)
	}
	s.keepFolderSet(set, in.Trashed)
	return set, page.Token, nil
}

// walkFolders finds every folder below root, one level at a time: one
// query per level asks for the folders whose parent is any folder of
// the level above. Only folders are asked for, so a shortcut to a folder
// is never followed. A tree past MaxUnderFolders is refused rather than
// searched in part, as copy_file refuses a tree past its budget.
func (s *Service) walkFolders(ctx context.Context, root *gdrive.File, trashed bool) (*folderSet, error) {
	set := &folderSet{root: root, ids: []string{root.ID}}
	if cannotList(root) {
		set.unlistable = append(set.unlistable, root.Name)
	}
	seen := map[string]bool{root.ID: true}
	for level := []string{root.ID}; len(level) > 0; {
		var next []string
		token := ""
		for {
			list, err := s.api.ListFiles(ctx, gapi.ListQuery{
				Q: folderLevelQuery(level, trashed), PageSize: gapi.MaxPageSize, PageToken: token,
				DriveID: root.DriveID, ResourceIDs: level, Fields: folderLevelFields,
			})
			if err != nil {
				return nil, wrap(err, "listing the folders under "+root.Name)
			}
			for _, f := range list.Files {
				if seen[f.ID] {
					continue
				}
				if len(set.ids) == MaxUnderFolders {
					return nil, Errorf(ClassInvalid, "%s holds more than %d folders, counting itself, and one "+
						"search covers at most that many. Search under one of the folders inside it, or use "+
						"in_folder for one folder's direct contents. Nothing was searched.", root.Name, MaxUnderFolders)
				}
				seen[f.ID] = true
				set.ids = append(set.ids, f.ID)
				next = append(next, f.ID)
				if cannotList(f) {
					set.unlistable = append(set.unlistable, f.Name)
				}
			}
			if list.NextPageToken == "" {
				break
			}
			token = list.NextPageToken
		}
		level = next
	}
	slices.Sort(set.ids)
	sum := sha256.Sum256([]byte(strings.Join(set.ids, ",")))
	set.digest = hex.EncodeToString(sum[:8])
	return set, nil
}

// folderLevelFields is what a level of the walk reads: a folder's id and
// name, and whether this account can list it.
const folderLevelFields = "nextPageToken,files(id,name,capabilities(canListChildren))"

// folderLevelQuery asks for the folders directly inside any of the
// given ones. A search of the trash walks trashed folders too, since
// what is inside a trashed folder is in the trash with it.
func folderLevelQuery(parents []string, trashed bool) string {
	terms := make([]string, 0, len(parents))
	for _, id := range parents {
		terms = append(terms, quote(id)+" in parents")
	}
	q := "mimeType = " + quote(gdrive.MimeFolder) + " and (" + strings.Join(terms, " or ") + ")"
	if !trashed {
		q += " and trashed = false"
	}
	return q
}

// cachedFolderSet returns the set a first page searched, when it is
// still kept and is the one the continuation names.
func (s *Service) cachedFolderSet(root, digest string, trashed bool) (*folderSet, bool) {
	if digest == "" {
		return nil, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	e := s.folders
	if e.value == nil || e.trashed != trashed || e.value.root.ID != root || e.value.digest != digest ||
		s.now().Sub(e.at) > folderSetTTL {
		return nil, false
	}
	return e.value, true
}

// keepFolderSet keeps one set for the next page. One is enough, because
// paging through a search is sequential.
func (s *Service) keepFolderSet(set *folderSet, trashed bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.folders = keptFolders{value: set, trashed: trashed, at: s.now()}
}

// keptFolders is the folder set the last search with under_folder
// walked, and whether it walked the trash.
type keptFolders struct {
	value   *folderSet
	trashed bool
	at      time.Time
}
