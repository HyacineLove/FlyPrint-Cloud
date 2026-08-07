package main

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

const (
	pdfFixtureSHA256 = "9dee20546540c12ccb46eab6d9d3cd87a3046a06b4c87f8ef58c0983e22cf2e6"
	pdfFixture       = "%PDF-1.4\n1 0 obj\n<< /Type /Catalog /Pages 2 0 R >>\nendobj\n2 0 obj\n<< /Type /Pages /Kids [3 0 R] /Count 1 >>\nendobj\n3 0 obj\n<< /Type /Page /Parent 2 0 R /MediaBox [0 0 72 72] >>\nendobj\ntrailer\n<< /Root 1 0 R >>\n%%EOF\n"
)

func docxFixture(t *testing.T) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for name, content := range map[string]string{
		"[Content_Types].xml": `<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"></Types>`,
		"word/document.xml":   `<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"></w:document>`,
	} {
		part, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(part, content); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func TestPDFUploadListDownloadIsUserIsolated(t *testing.T) {
	config := testConfiguration(t)
	server, err := newServer(config, testTokenVerifier())
	if err != nil {
		t.Fatal(err)
	}
	defer server.close()
	server.now = func() time.Time { return time.Unix(1100, 0).UTC() }
	handler := server.Handler()

	userOneToken := buildLiteralToken(t, validLiteralClaims())
	contextResponse := performJSONRequest(t, handler, http.MethodPost, "/api/v1/upload-contexts", userOneToken, nil)
	if contextResponse.Code != http.StatusCreated {
		t.Fatalf("create upload context: status=%d body=%s", contextResponse.Code, contextResponse.Body.String())
	}
	var createdContext struct {
		UploadContext string `json:"upload_context"`
	}
	decodeResponseJSON(t, contextResponse, &createdContext)
	if createdContext.UploadContext == "" {
		t.Fatal("upload context is empty")
	}

	var multipartBody bytes.Buffer
	writer := multipart.NewWriter(&multipartBody)
	fileHeader := make(textproto.MIMEHeader)
	fileHeader.Set("Content-Disposition", `form-data; name="file"; filename="sample.pdf"`)
	fileHeader.Set("Content-Type", "application/pdf")
	filePart, err := writer.CreatePart(fileHeader)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(filePart, pdfFixture); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	uploadRequest := httptest.NewRequest(http.MethodPost, "/api/v1/files", &multipartBody)
	uploadRequest.Header.Set("Authorization", "Bearer "+createdContext.UploadContext)
	uploadRequest.Header.Set("Content-Type", writer.FormDataContentType())
	uploadResponse := httptest.NewRecorder()
	handler.ServeHTTP(uploadResponse, uploadRequest)
	if uploadResponse.Code != http.StatusCreated {
		t.Fatalf("upload: status=%d body=%s", uploadResponse.Code, uploadResponse.Body.String())
	}
	var uploaded struct {
		File struct {
			ID     string `json:"id"`
			Name   string `json:"name"`
			SHA256 string `json:"sha256"`
		} `json:"file"`
	}
	decodeResponseJSON(t, uploadResponse, &uploaded)
	if uploaded.File.ID == "" || uploaded.File.Name != "sample.pdf" || uploaded.File.SHA256 != pdfFixtureSHA256 {
		t.Fatalf("uploaded=%#v", uploaded.File)
	}

	listResponse := performJSONRequest(t, handler, http.MethodGet, "/api/v1/files?page=1&page_size=20", userOneToken, nil)
	if listResponse.Code != http.StatusOK {
		t.Fatalf("list: status=%d body=%s", listResponse.Code, listResponse.Body.String())
	}
	var list struct {
		Items []struct {
			ID     string `json:"id"`
			Name   string `json:"name"`
			SHA256 string `json:"sha256"`
		} `json:"items"`
		Total int `json:"total"`
	}
	decodeResponseJSON(t, listResponse, &list)
	if list.Total != 1 || len(list.Items) != 1 ||
		list.Items[0].Name != "sample.pdf" || list.Items[0].SHA256 != pdfFixtureSHA256 {
		t.Fatalf("list=%#v", list)
	}

	downloadResponse := performJSONRequest(t, handler, http.MethodGet, "/api/v1/files/"+uploaded.File.ID+"/content", userOneToken, nil)
	if downloadResponse.Code != http.StatusOK {
		t.Fatalf("download: status=%d body=%s", downloadResponse.Code, downloadResponse.Body.String())
	}
	if downloadResponse.Header().Get("X-Content-SHA256") != pdfFixtureSHA256 {
		t.Fatalf("download sha256=%q", downloadResponse.Header().Get("X-Content-SHA256"))
	}
	if downloadResponse.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("download cache-control=%q", downloadResponse.Header().Get("Cache-Control"))
	}
	if downloadResponse.Header().Get("Content-Type") != "application/pdf" ||
		downloadResponse.Header().Get("Content-Length") != strconv.Itoa(len(pdfFixture)) ||
		!strings.Contains(downloadResponse.Header().Get("Content-Disposition"), "sample.pdf") {
		t.Fatalf("download headers=%v", downloadResponse.Header())
	}
	if !bytes.Equal(downloadResponse.Body.Bytes(), []byte(pdfFixture)) {
		t.Fatal("download changed bytes")
	}
	afterDownload := performJSONRequest(t, handler, http.MethodGet, "/api/v1/files?page=1&page_size=20", userOneToken, nil)
	var downloadedList struct {
		Items []struct {
			LastDownloadedAt *time.Time `json:"last_downloaded_at"`
		} `json:"items"`
	}
	decodeResponseJSON(t, afterDownload, &downloadedList)
	if len(downloadedList.Items) != 1 || downloadedList.Items[0].LastDownloadedAt == nil {
		t.Fatalf("last_downloaded_at was not updated: %#v", downloadedList)
	}

	userTwoClaims := validLiteralClaims()
	userTwoClaims["sub"] = "user-2"
	userTwoClaims["jti"] = "token-id-2"
	userTwoToken := buildLiteralToken(t, userTwoClaims)
	otherList := performJSONRequest(t, handler, http.MethodGet, "/api/v1/files?page=1&page_size=20", userTwoToken, nil)
	if otherList.Code != http.StatusOK {
		t.Fatalf("other list: status=%d body=%s", otherList.Code, otherList.Body.String())
	}
	var other struct {
		Items []json.RawMessage `json:"items"`
		Total int               `json:"total"`
	}
	decodeResponseJSON(t, otherList, &other)
	if other.Total != 0 || len(other.Items) != 0 {
		t.Fatalf("other list=%#v", other)
	}

	otherDownload := performJSONRequest(t, handler, http.MethodGet, "/api/v1/files/"+uploaded.File.ID+"/content", userTwoToken, nil)
	if otherDownload.Code != http.StatusNotFound ||
		!strings.Contains(otherDownload.Body.String(), `"code":"file_not_found"`) {
		t.Fatalf("other download: status=%d body=%s", otherDownload.Code, otherDownload.Body.String())
	}
}

func TestPDFStoreRejectsUnsupportedTypeSignatureAndOversize(t *testing.T) {
	now := time.Unix(1100, 0).UTC()

	t.Run("declared media type", func(t *testing.T) {
		store := openTestFileStore(t, testConfiguration(t))
		_, err := store.uploadFile(context.Background(), "user-1", "sample.pdf", "text/plain", strings.NewReader(pdfFixture), now)
		if !errors.Is(err, errUnsupportedFileType) {
			t.Fatalf("error=%v", err)
		}
	})

	t.Run("file signature", func(t *testing.T) {
		store := openTestFileStore(t, testConfiguration(t))
		_, err := store.uploadFile(context.Background(), "user-1", "sample.pdf", "application/pdf", strings.NewReader("not a PDF"), now)
		if !errors.Is(err, errUnsupportedFileType) {
			t.Fatalf("error=%v", err)
		}
	})

	t.Run("file size", func(t *testing.T) {
		config := testConfiguration(t)
		config.MaxFileSizeBytes = int64(len(pdfFixture) - 1)
		store := openTestFileStore(t, config)
		_, err := store.uploadFile(context.Background(), "user-1", "sample.pdf", "application/pdf", strings.NewReader(pdfFixture), now)
		if !errors.Is(err, errFileTooLarge) {
			t.Fatalf("error=%v", err)
		}
	})
}

func TestFileStoreAcceptsSupportedPDFImageAndDOCXTypes(t *testing.T) {
	now := time.Unix(1100, 0).UTC()
	testCases := []struct {
		name      string
		fileName  string
		mediaType string
		content   []byte
	}{
		{name: "pdf", fileName: "sample.pdf", mediaType: "application/pdf", content: []byte(pdfFixture)},
		{name: "png", fileName: "sample.png", mediaType: "image/png", content: append([]byte("\x89PNG\r\n\x1a\n"), []byte("fixture")...)},
		{name: "jpeg", fileName: "sample.jpg", mediaType: "image/jpeg", content: []byte("\xff\xd8\xff\xe0fixture")},
		{name: "docx", fileName: "sample.docx", mediaType: "application/vnd.openxmlformats-officedocument.wordprocessingml.document", content: docxFixture(t)},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			store := openTestFileStore(t, testConfiguration(t))
			item, err := store.uploadFile(
				context.Background(), "user-1", testCase.fileName, testCase.mediaType,
				bytes.NewReader(testCase.content), now,
			)
			if err != nil {
				t.Fatal(err)
			}
			if item.Name != testCase.fileName || item.MediaType != testCase.mediaType || item.Size != int64(len(testCase.content)) {
				t.Fatalf("item=%#v", item)
			}
		})
	}
}

func TestFileStoreRejectsMismatchedImageAndDOCXContent(t *testing.T) {
	now := time.Unix(1100, 0).UTC()
	testCases := []struct {
		name      string
		fileName  string
		mediaType string
		content   string
	}{
		{name: "png", fileName: "sample.png", mediaType: "image/png", content: "not png"},
		{name: "jpeg", fileName: "sample.jpg", mediaType: "image/jpeg", content: "not jpeg"},
		{name: "docx", fileName: "sample.docx", mediaType: "application/vnd.openxmlformats-officedocument.wordprocessingml.document", content: "not docx"},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			store := openTestFileStore(t, testConfiguration(t))
			_, err := store.uploadFile(
				context.Background(), "user-1", testCase.fileName, testCase.mediaType,
				strings.NewReader(testCase.content), now,
			)
			if !errors.Is(err, errUnsupportedFileType) {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func TestFileStoreEvictsLeastRecentlyUsedFileForUserLimits(t *testing.T) {
	config := testConfiguration(t)
	config.MaxFilesPerUser = 2
	store := openTestFileStore(t, config)
	ctx := context.Background()
	base := time.Unix(1100, 0).UTC()

	first, err := store.uploadFile(ctx, "user-1", "first.pdf", "application/pdf", strings.NewReader(pdfFixture), base)
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.uploadFile(ctx, "user-1", "second.pdf", "application/pdf", strings.NewReader(pdfFixture), base.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.markDownloaded(ctx, "user-1", first.ID, base.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	third, err := store.uploadFile(ctx, "user-1", "third.pdf", "application/pdf", strings.NewReader(pdfFixture), base.Add(3*time.Minute))
	if err != nil {
		t.Fatal(err)
	}

	files, err := store.list(ctx, "user-1", 1, 20)
	if err != nil {
		t.Fatal(err)
	}
	if files.Total != 2 || len(files.Items) != 2 {
		t.Fatalf("files=%#v", files)
	}
	ids := map[string]bool{files.Items[0].ID: true, files.Items[1].ID: true}
	if !ids[first.ID] || !ids[third.ID] || ids[second.ID] {
		t.Fatalf("remaining ids=%v", ids)
	}
}

func TestFileStoreGlobalEvictionProtectsActiveDownload(t *testing.T) {
	config := testConfiguration(t)
	config.MaxFilesPerUser = 10
	config.MaxBytesPerUser = 10 * int64(len(pdfFixture))
	config.MaxTotalBytes = 2 * int64(len(pdfFixture))
	store := openTestFileStore(t, config)
	ctx := context.Background()
	base := time.Unix(1100, 0).UTC()

	protected, err := store.uploadFile(ctx, "user-1", "protected.pdf", "application/pdf", strings.NewReader(pdfFixture), base)
	if err != nil {
		t.Fatal(err)
	}
	evictable, err := store.uploadFile(ctx, "user-2", "evictable.pdf", "application/pdf", strings.NewReader(pdfFixture), base.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	_, activeDownload, err := store.openDownload(ctx, "user-1", protected.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer activeDownload.Close()

	if _, err := store.uploadFile(ctx, "user-3", "new.pdf", "application/pdf", strings.NewReader(pdfFixture), base.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, file, err := store.openDownload(ctx, "user-2", evictable.ID); !errors.Is(err, errFileNotFound) {
		if file != nil {
			_ = file.Close()
		}
		t.Fatalf("evictable download error=%v", err)
	}
	if _, file, err := store.openDownload(ctx, "user-1", protected.ID); err != nil {
		t.Fatalf("protected download error=%v", err)
	} else {
		_ = file.Close()
	}
}

func TestFileStoreCleanupRemovesExpiredFilesAndReconcilesStartup(t *testing.T) {
	config := testConfiguration(t)
	ctx := context.Background()
	base := time.Unix(1100, 0).UTC()
	store, err := openFileStore(config)
	if err != nil {
		t.Fatal(err)
	}

	expired, err := store.uploadFile(ctx, "user-1", "expired.pdf", "application/pdf", strings.NewReader(pdfFixture), base)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.cleanup(ctx, base.Add(config.FileTTL+time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, file, err := store.openDownload(ctx, "user-1", expired.ID); !errors.Is(err, errFileNotFound) {
		if file != nil {
			_ = file.Close()
		}
		t.Fatalf("expired download error=%v", err)
	}

	live, err := store.uploadFile(ctx, "user-1", "missing.pdf", "application/pdf", strings.NewReader(pdfFixture), base)
	if err != nil {
		t.Fatal(err)
	}
	var relativePath string
	if err := store.db.QueryRow(`SELECT relative_path FROM files WHERE id = ?`, live.ID).Scan(&relativePath); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(config.DataDir, filepath.FromSlash(relativePath))); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(config.DataDir, "tmp", "stale.part"), []byte("partial"), 0o640); err != nil {
		t.Fatal(err)
	}
	orphanPath := filepath.Join(config.DataDir, "files", "orphan.pdf")
	if err := os.WriteFile(orphanPath, []byte(pdfFixture), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := store.close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := openFileStore(config)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.close()
	files, err := reopened.list(ctx, "user-1", 1, 20)
	if err != nil {
		t.Fatal(err)
	}
	if files.Total != 0 || len(files.Items) != 0 {
		t.Fatalf("files after reconciliation=%#v", files)
	}
	for _, path := range []string{filepath.Join(config.DataDir, "tmp", "stale.part"), orphanPath} {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("path still exists: %s err=%v", path, err)
		}
	}
}

func openTestFileStore(t *testing.T, config configuration) *fileStore {
	t.Helper()
	store, err := openFileStore(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.close(); err != nil {
			t.Error(err)
		}
	})
	return store
}

func performJSONRequest(t *testing.T, handler http.Handler, method, target, bearer string, body io.Reader) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, target, body)
	if bearer != "" {
		request.Header.Set("Authorization", "Bearer "+bearer)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func decodeResponseJSON(t *testing.T, response *httptest.ResponseRecorder, target any) {
	t.Helper()
	if err := json.Unmarshal(response.Body.Bytes(), target); err != nil {
		t.Fatalf("decode response %q: %v", response.Body.String(), err)
	}
}
