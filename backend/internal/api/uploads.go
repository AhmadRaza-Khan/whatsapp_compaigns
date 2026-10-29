package api

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/gin-gonic/gin"
)

const (
	maxImageBytes    = 5 << 20  // WhatsApp image headers: JPEG/PNG up to 5 MB
	maxDocumentBytes = 25 << 20 // PDF headers; WhatsApp allows more, but keep campaign files small
)

var (
	mediaName  = regexp.MustCompile(`^[a-f0-9]{32}\.(jpg|png|pdf)$`)
	unsafeName = regexp.MustCompile(`[^\p{L}\p{N}._ -]+`)
)

// readUpload reads the "file" form field, enforcing a size limit.
func readUpload(c *gin.Context, limit int64, tooBig string) ([]byte, string, bool) {
	fh, err := c.FormFile("file")
	if err != nil {
		fail(c, 400, "choose a file to upload")
		return nil, "", false
	}
	if fh.Size > limit {
		fail(c, 400, tooBig)
		return nil, "", false
	}
	f, err := fh.Open()
	if err != nil {
		serverError(c, err)
		return nil, "", false
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		serverError(c, err)
		return nil, "", false
	}
	if int64(len(data)) > limit {
		fail(c, 400, tooBig)
		return nil, "", false
	}
	return data, fh.Filename, true
}

// saveMedia writes the file under a random name and returns its public URL,
// which WhatsApp fetches when each message is sent.
func (s *Server) saveMedia(c *gin.Context, data []byte, ext string) (string, bool) {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	name := hex.EncodeToString(b) + "." + ext
	if err := os.MkdirAll(s.cfg.UploadDir, 0o755); err != nil {
		serverError(c, err)
		return "", false
	}
	final := filepath.Join(s.cfg.UploadDir, name)
	tmp := final + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		serverError(c, err)
		return "", false
	}
	if err := os.Rename(tmp, final); err != nil {
		serverError(c, err)
		return "", false
	}
	return s.cfg.PublicURL + "/media/" + name, true
}

func (s *Server) uploadImage(c *gin.Context) {
	data, _, ok := readUpload(c, maxImageBytes, "image must be 5 MB or smaller (WhatsApp limit)")
	if !ok {
		return
	}
	// Decide the type from the bytes, not the file name.
	var ext string
	switch http.DetectContentType(data) {
	case "image/jpeg":
		ext = "jpg"
	case "image/png":
		ext = "png"
	default:
		fail(c, 400, "only JPG and PNG images can be sent on WhatsApp")
		return
	}
	if u, ok := s.saveMedia(c, data, ext); ok {
		c.JSON(201, gin.H{"url": u, "size": len(data)})
	}
}

func (s *Server) uploadDocument(c *gin.Context) {
	data, original, ok := readUpload(c, maxDocumentBytes, "PDF must be 25 MB or smaller")
	if !ok {
		return
	}
	if http.DetectContentType(data) != "application/pdf" {
		fail(c, 400, "only PDF files can be used as a document header")
		return
	}
	if u, ok := s.saveMedia(c, data, "pdf"); ok {
		c.JSON(201, gin.H{"url": u, "size": len(data), "filename": documentName(original)})
	}
}

// documentName is the name customers see on the PDF in WhatsApp.
func documentName(original string) string {
	base := strings.TrimSuffix(filepath.Base(original), filepath.Ext(original))
	base = strings.TrimSpace(unsafeName.ReplaceAllString(base, ""))
	if base == "" || base == "." {
		base = "document"
	}
	if r := []rune(base); len(r) > 80 {
		base = string(r[:80])
	}
	return base + ".pdf"
}

// serveMedia serves uploaded files publicly (WhatsApp must be able to fetch
// them without logging in). Names are random, so they can't be guessed.
func (s *Server) serveMedia(c *gin.Context) {
	name := c.Param("name")
	if !mediaName.MatchString(name) {
		c.Status(404)
		return
	}
	path := filepath.Join(s.cfg.UploadDir, name)
	if _, err := os.Stat(path); err != nil {
		c.Status(404)
		return
	}
	c.Header("Cache-Control", "public, max-age=31536000, immutable")
	if strings.HasSuffix(name, ".pdf") {
		c.Header("Content-Type", "application/pdf")
		c.Header("Content-Disposition", fmt.Sprintf(`inline; filename=%q`, name))
	}
	c.File(path)
}
