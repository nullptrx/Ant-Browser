package backend

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"ant-chrome/backend/internal/browser"
	"ant-chrome/backend/internal/database"
)

func readBookmarkBar(t *testing.T, userDataDir string) (top []string, nested []string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(userDataDir, "Default", "Bookmarks"))
	if err != nil {
		t.Fatalf("read bookmarks: %v", err)
	}
	var root struct {
		Roots struct {
			BookmarkBar struct {
				Children []struct {
					Type     string `json:"type"`
					URL      string `json:"url"`
					Children []struct {
						URL string `json:"url"`
					} `json:"children"`
				} `json:"children"`
			} `json:"bookmark_bar"`
		} `json:"roots"`
	}
	if err := json.Unmarshal(data, &root); err != nil {
		t.Fatalf("parse bookmarks: %v", err)
	}
	for _, child := range root.Roots.BookmarkBar.Children {
		if child.Type == "folder" {
			for _, n := range child.Children {
				nested = append(nested, n.URL)
			}
			continue
		}
		top = append(top, child.URL)
	}
	return top, nested
}

func TestApplyDefaultBookmarksSkipsAndRemovesDisabled(t *testing.T) {
	userDataDir := t.TempDir()
	if err := applyDefaultBookmarks(userDataDir, []BrowserBookmark{
		{Name: "A", URL: "https://a.example/"},
		{Name: "B", URL: "https://b.example/"},
		{Name: "C", URL: "https://c.example/", Disabled: true},
	}); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if top, _ := readBookmarkBar(t, userDataDir); len(top) != 2 || top[0] != "https://a.example/" || top[1] != "https://b.example/" {
		t.Fatalf("top = %v, want only enabled bookmarks", top)
	}

	// 用户在子文件夹里放了一份 B，取消勾选后只移除书签栏第一层的默认书签。
	path := filepath.Join(userDataDir, "Default", "Bookmarks")
	data, _ := os.ReadFile(path)
	var root map[string]interface{}
	_ = json.Unmarshal(data, &root)
	bar := root["roots"].(map[string]interface{})["bookmark_bar"].(map[string]interface{})
	bar["children"] = append(bar["children"].([]interface{}), map[string]interface{}{
		"type": "folder", "name": "mine", "children": []interface{}{map[string]interface{}{"type": "url", "name": "B", "url": "https://b.example/"}},
	})
	out, _ := json.Marshal(root)
	_ = os.WriteFile(path, out, 0644)

	if err := applyDefaultBookmarks(userDataDir, []BrowserBookmark{
		{Name: "A", URL: "https://a.example/"},
		{Name: "B", URL: "https://b.example/", Disabled: true},
	}); err != nil {
		t.Fatalf("apply: %v", err)
	}
	top, nested := readBookmarkBar(t, userDataDir)
	if len(top) != 1 || top[0] != "https://a.example/" {
		t.Fatalf("top = %v, want B removed from bookmark bar", top)
	}
	if len(nested) != 1 || nested[0] != "https://b.example/" {
		t.Fatalf("nested = %v, want user folder untouched", nested)
	}
}

func TestNormalizeBookmarkListKeepsDisabledForBuiltins(t *testing.T) {
	list := normalizeBookmarkList([]BrowserBookmark{
		{Name: "指纹检测", URL: fingerprintCheckBookmarkURL, Disabled: true},
		{Name: "IP.SB", URL: "https://ip.sb/", Disabled: true},
	})
	disabled := map[string]bool{}
	for _, item := range list {
		disabled[item.URL] = item.Disabled
	}
	if !disabled[fingerprintCheckBookmarkURL] || !disabled["https://ip.sb/"] {
		t.Fatalf("disabled state lost: %#v", list)
	}
	if disabled["https://ippure.com/"] {
		t.Fatalf("missing builtin should default to enabled: %#v", list)
	}
}

func TestSQLiteBookmarkDAOPersistsDisabled(t *testing.T) {
	db, err := database.NewDB(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	if err := db.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	dao := browser.NewSQLiteBookmarkDAO(db.GetConn())
	if err := dao.ReplaceAll([]BrowserBookmark{
		{Name: "A", URL: "https://a.example/"},
		{Name: "B", URL: "https://b.example/", Disabled: true, OpenOnStart: true},
	}); err != nil {
		t.Fatalf("replace: %v", err)
	}
	list, err := dao.List()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 2 || list[0].Disabled || !list[1].Disabled || !list[1].OpenOnStart {
		t.Fatalf("list = %#v", list)
	}
}
