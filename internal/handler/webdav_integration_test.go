package handler

import (
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"src.solsynth.dev/sosys/filesystem/internal/database"
	"src.solsynth.dev/sosys/filesystem/internal/service"
)

// newWebDAVTestServer sets up a real HTTP server with the WebDAV handler.
// Auth is bypassed by injecting the account ID into the gin context.
func newWebDAVTestServer(t *testing.T, accountID uuid.UUID, files *service.FileService) *httptest.Server {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	handler := func(c *gin.Context) {
		c.Set(WebDAVAccountIDKey, accountID.String())
		handleWebDAV(c, files, nil, nil, "/webdav")
	}
	r.Any("/webdav/*path", handler)
	r.Any("/webdav", handler)
	for _, method := range []string{"PROPFIND", "PROPPATCH", "MKCOL", "COPY", "MOVE", "LOCK", "UNLOCK"} {
		r.Handle(method, "/webdav/*path", handler)
		r.Handle(method, "/webdav", handler)
	}
	return httptest.NewServer(r)
}

// propfind sends a PROPFIND request and returns the response.
func propfind(t *testing.T, url, depth string) *http.Response {
	t.Helper()
	req, err := http.NewRequest("PROPFIND", url, strings.NewReader(`<?xml version="1.0" encoding="utf-8"?>
<D:propfind xmlns:D="DAV:">
  <D:allprop/>
</D:propfind>`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Depth", depth)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

// multistatus is the XML structure for a DAV multistatus response.
type multistatus struct {
	Responses []davResponse `xml:"response"`
}

type davResponse struct {
	Href     string       `xml:"href"`
	PropStat []davPropStat `xml:"propstat"`
}

type davPropStat struct {
	Prop   davProp `xml:"prop"`
	Status string  `xml:"status"`
}

type davProp struct {
	DisplayName string `xml:"displayname"`
	ResType     struct {
		Collection *struct{} `xml:"collection"`
	} `xml:"resourcetype"`
	ContentLength string `xml:"getcontentlength"`
}

func TestWebDAVPropfindRoot(t *testing.T) {
	db := openHandlerTestDB(t, &database.CloudFile{}, &database.FileObject{}, &database.FilePool{}, &database.FilePermission{}, &database.FileLock{})
	files := service.NewFileService(&database.DB{DB: db}, nil)
	accountID := uuid.New()

	// Seed: 2 root items (1 file, 1 folder) + 1 unindexed (must be hidden)
	rootFile := database.CloudFile{ID: database.NewID(), Name: "readme.txt", AccountID: accountID, Indexed: true}
	rootFolder := database.CloudFile{ID: database.NewID(), Name: "Documents", AccountID: accountID, Indexed: true, IsFolder: true}
	child := database.CloudFile{ID: database.NewID(), Name: "notes.txt", AccountID: accountID, ParentID: &rootFolder.ID, Indexed: true}
	unindexed := database.CloudFile{ID: database.NewID(), Name: "hidden", AccountID: accountID, Indexed: false}

	for _, f := range []database.CloudFile{rootFile, rootFolder, child, unindexed} {
		if err := db.Create(&f).Error; err != nil {
			t.Fatalf("create %s: %v", f.Name, err)
		}
	}

	srv := newWebDAVTestServer(t, accountID, files)
	defer srv.Close()

	// PROPFIND Depth:1 — this is what "rclone lsd" sends
	resp := propfind(t, srv.URL+"/webdav/", "1")
	defer resp.Body.Close()

	if resp.StatusCode != 207 {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("PROPFIND / status = %d, want 207; body: %s", resp.StatusCode, body)
	}

	body, _ := io.ReadAll(resp.Body)
	var ms multistatus
	if err := xml.Unmarshal(body, &ms); err != nil {
		t.Fatalf("unmarshal multistatus: %v\nbody:\n%s", err, body)
	}

	// Should have 3 responses: root + readme.txt + Documents
	if len(ms.Responses) != 3 {
		t.Fatalf("got %d responses, want 3 (root + 2 children)", len(ms.Responses))
		for _, r := range ms.Responses {
			t.Logf("  href=%s", r.Href)
		}
	}

	names := map[string]bool{}
	for _, r := range ms.Responses {
		for _, ps := range r.PropStat {
			name := ps.Prop.DisplayName
			isDir := ps.Prop.ResType.Collection != nil
			t.Logf("  href=%s  name=%s  dir=%v", r.Href, name, isDir)
			if name != "" {
				names[name] = isDir
			}
		}
	}

	if _, ok := names["readme.txt"]; !ok {
		t.Error("missing readme.txt")
	}
	if isDir, ok := names["Documents"]; !ok {
		t.Error("missing Documents")
	} else if !isDir {
		t.Error("Documents should be a directory")
	}
}

func TestWebDAVPropfindSubdir(t *testing.T) {
	db := openHandlerTestDB(t, &database.CloudFile{}, &database.FileObject{}, &database.FilePool{}, &database.FilePermission{}, &database.FileLock{})
	files := service.NewFileService(&database.DB{DB: db}, nil)
	accountID := uuid.New()

	folder := database.CloudFile{ID: database.NewID(), Name: "Photos", AccountID: accountID, Indexed: true, IsFolder: true}
	pic1 := database.CloudFile{ID: database.NewID(), Name: "cat.jpg", AccountID: accountID, ParentID: &folder.ID, Indexed: true}
	pic2 := database.CloudFile{ID: database.NewID(), Name: "dog.jpg", AccountID: accountID, ParentID: &folder.ID, Indexed: true}

	for _, f := range []database.CloudFile{folder, pic1, pic2} {
		if err := db.Create(&f).Error; err != nil {
			t.Fatalf("create %s: %v", f.Name, err)
		}
	}

	srv := newWebDAVTestServer(t, accountID, files)
	defer srv.Close()

	resp := propfind(t, srv.URL+"/webdav/Photos/", "1")
	defer resp.Body.Close()

	if resp.StatusCode != 207 {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("PROPFIND /Photos/ status = %d, want 207; body: %s", resp.StatusCode, body)
	}

	body, _ := io.ReadAll(resp.Body)
	var ms multistatus
	if err := xml.Unmarshal(body, &ms); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if len(ms.Responses) != 3 {
		t.Fatalf("got %d responses, want 3 (dir + 2 files)", len(ms.Responses))
	}

	names := map[string]bool{}
	for _, r := range ms.Responses {
		for _, ps := range r.PropStat {
			name := ps.Prop.DisplayName
			isDir := ps.Prop.ResType.Collection != nil
			if name != "" {
				names[name] = isDir
			}
		}
	}

	if _, ok := names["cat.jpg"]; !ok {
		t.Error("missing cat.jpg")
	}
	if _, ok := names["dog.jpg"]; !ok {
		t.Error("missing dog.jpg")
	}
}

func TestWebDAVPropfindNonexistent(t *testing.T) {
	db := openHandlerTestDB(t, &database.CloudFile{}, &database.FileObject{}, &database.FilePool{}, &database.FilePermission{}, &database.FileLock{})
	files := service.NewFileService(&database.DB{DB: db}, nil)
	accountID := uuid.New()

	srv := newWebDAVTestServer(t, accountID, files)
	defer srv.Close()

	resp := propfind(t, srv.URL+"/webdav/nope/", "1")
	defer resp.Body.Close()

	if resp.StatusCode != 404 {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("PROPFIND /nope/ status = %d, want 404; body: %s", resp.StatusCode, body)
	}
}

func TestWebDAVMkcolAndList(t *testing.T) {
	db := openHandlerTestDB(t, &database.CloudFile{}, &database.FileObject{}, &database.FilePool{}, &database.FilePermission{}, &database.FileLock{})
	files := service.NewFileService(&database.DB{DB: db}, nil)
	accountID := uuid.New()

	srv := newWebDAVTestServer(t, accountID, files)
	defer srv.Close()

	// MKCOL to create a directory
	req, _ := http.NewRequest("MKCOL", srv.URL+"/webdav/NewFolder", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 201 {
		t.Fatalf("MKCOL status = %d, want 201", resp.StatusCode)
	}

	// Now PROPFIND root — should see the new folder
	proResp := propfind(t, srv.URL+"/webdav/", "1")
	defer proResp.Body.Close()

	body, _ := io.ReadAll(proResp.Body)
	var ms multistatus
	if err := xml.Unmarshal(body, &ms); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	found := false
	for _, r := range ms.Responses {
		for _, ps := range r.PropStat {
			if ps.Prop.DisplayName == "NewFolder" && ps.Prop.ResType.Collection != nil {
				found = true
			}
		}
	}
	if !found {
		t.Error("NewFolder not found after MKCOL")
		for _, r := range ms.Responses {
			for _, ps := range r.PropStat {
				t.Logf("  href=%s name=%s dir=%v", r.Href, ps.Prop.DisplayName, ps.Prop.ResType.Collection != nil)
			}
		}
	}
}

func TestWebDAVOptions(t *testing.T) {
	db := openHandlerTestDB(t, &database.CloudFile{}, &database.FileObject{}, &database.FilePool{}, &database.FilePermission{}, &database.FileLock{})
	files := service.NewFileService(&database.DB{DB: db}, nil)
	accountID := uuid.New()

	srv := newWebDAVTestServer(t, accountID, files)
	defer srv.Close()

	req, _ := http.NewRequest("OPTIONS", srv.URL+"/webdav/", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Fatalf("OPTIONS status = %d, want 200", resp.StatusCode)
	}
	allow := resp.Header.Get("DAV")
	if allow == "" {
		t.Error("missing DAV header in OPTIONS response")
	}
	t.Logf("DAV: %s", allow)
}

func TestWebDAVRootReturns200(t *testing.T) {
	// This verifies the core fix: PROPFIND /webdav/ returns 207, not 404.
	// Before the fix, walkFS called OpenFile("/") which failed with "root is not a file".
	db := openHandlerTestDB(t, &database.CloudFile{}, &database.FileObject{}, &database.FilePool{}, &database.FilePermission{}, &database.FileLock{})
	files := service.NewFileService(&database.DB{DB: db}, nil)
	accountID := uuid.New()

	srv := newWebDAVTestServer(t, accountID, files)
	defer srv.Close()

	resp, err := http.DefaultClient.Do(func() *http.Request {
		r, _ := http.NewRequest("PROPFIND", srv.URL+"/webdav/", strings.NewReader(`<?xml version="1.0" encoding="utf-8"?>
<D:propfind xmlns:D="DAV:"><D:allprop/></D:propfind>`))
		r.Header.Set("Depth", "0")
		return r
	}())
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	if resp.StatusCode != 207 {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("PROPFIND /webdav/ Depth:0 status = %d, want 207; body:\n%s", resp.StatusCode, body)
	}
	fmt.Println("PROPFIND /webdav/ returns 207 Multi-Status ✓")
}
