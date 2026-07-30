package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"strconv"
	"strings"
	"testing"
	"time"
)

const (
	pdfFixtureSHA256 = "9dee20546540c12ccb46eab6d9d3cd87a3046a06b4c87f8ef58c0983e22cf2e6"
	pdfFixture       = "%PDF-1.4\n1 0 obj\n<< /Type /Catalog /Pages 2 0 R >>\nendobj\n2 0 obj\n<< /Type /Pages /Kids [3 0 R] /Count 1 >>\nendobj\n3 0 obj\n<< /Type /Page /Parent 2 0 R /MediaBox [0 0 72 72] >>\nendobj\ntrailer\n<< /Root 1 0 R >>\n%%EOF\n"
)

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
		_, err := store.uploadPDF(context.Background(), "user-1", "sample.pdf", "text/plain", strings.NewReader(pdfFixture), now)
		if !errors.Is(err, errUnsupportedFileType) {
			t.Fatalf("error=%v", err)
		}
	})

	t.Run("file signature", func(t *testing.T) {
		store := openTestFileStore(t, testConfiguration(t))
		_, err := store.uploadPDF(context.Background(), "user-1", "sample.pdf", "application/pdf", strings.NewReader("not a PDF"), now)
		if !errors.Is(err, errUnsupportedFileType) {
			t.Fatalf("error=%v", err)
		}
	})

	t.Run("file size", func(t *testing.T) {
		config := testConfiguration(t)
		config.MaxFileSizeBytes = int64(len(pdfFixture) - 1)
		store := openTestFileStore(t, config)
		_, err := store.uploadPDF(context.Background(), "user-1", "sample.pdf", "application/pdf", strings.NewReader(pdfFixture), now)
		if !errors.Is(err, errFileTooLarge) {
			t.Fatalf("error=%v", err)
		}
	})
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
