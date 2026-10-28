package api

import (
	"crypto/rand"
	"encoding/hex"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"

	"github.com/gin-gonic/gin"
)

// WhatsApp image headers accept JPEG or PNG up to 5 MB.
const maxImageBytes = 5 << 20

var mediaName = regexp.MustCompile(`^[a-f0-9]{32}\.(jpg|png)$`)

// uploadImage stores a header image on the server and returns its public URL,
// which WhatsApp fetches when each message is sent.
func (s *Server) uploadImage(c *gin.Context) {
	fh, err := c.FormFile("file")
	if err != nil {
		fail(c, 400, "choose an image to upload")
		return
	}
	if fh.Size > maxImageBytes {
		fail(c, 400, "image must be 5 MB or smaller (WhatsApp limit)")
		return
	}
	f, err := fh.Open()
	if err != nil {
		serverError(c, err)
		return
	}
	data, err := io.ReadAll(io.LimitReader(f, maxImageBytes+1))
	f.Close()
	if err != nil {
		serverError(c, err)
		return
	}
	if len(data) > maxImageBytes {
		fail(c, 400, "image must be 5 MB or smaller (WhatsApp limit)")
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

	b := make([]byte, 16)
	_, _ = rand.Read(b)
	name := hex.EncodeToString(b) + "." + ext
	if err := os.MkdirAll(s.cfg.UploadDir, 0o755); err != nil {
		serverError(c, err)
		return
	}
	final := filepath.Join(s.cfg.UploadDir, name)
	tmp := final + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		serverError(c, err)
		return
	}
	if err := os.Rename(tmp, final); err != nil {
		serverError(c, err)
		return
	}
	c.JSON(201, gin.H{"url": s.cfg.PublicURL + "/media/" + name, "size": len(data)})
}

// serveMedia serves uploaded images publicly (WhatsApp must be able to fetch
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
	c.File(path)
}
