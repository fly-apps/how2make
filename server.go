package main

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"image"
	"image/jpeg"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/a-h/templ"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/disintegration/imaging"
	"github.com/gomarkdown/markdown"
	"github.com/gomarkdown/markdown/html"
	"github.com/gomarkdown/markdown/parser"
	"within.website/x/web/ollama"
	"within.website/x/xess"
)

func ParseMultipartFile(r *http.Request) ([]byte, error) {
	err := r.ParseMultipartForm(32 << 20) // 32MB
	if err != nil {
		return nil, err
	}

	file, _, err := r.FormFile("image")
	if err != nil {
		return nil, err
	}
	defer file.Close()

	fileBytes, err := io.ReadAll(file)
	if err != nil {
		return nil, err
	}

	return fileBytes, nil
}

type Server struct {
	cli *ollama.Client
	s3  *s3.Client
}

func (s *Server) NotFound(w http.ResponseWriter, r *http.Request) {
	slog.Error("not found", "method", r.Method, "path", r.URL.Path)
	templ.Handler(
		xess.Base(
			"404 Not Found",
			headArea(),
			nil,
			NotFound(r.URL.Path),
			footer(),
		),
		templ.WithStatus(http.StatusNotFound),
	).ServeHTTP(w, r)
}

func (s *Server) Error(w http.ResponseWriter, r *http.Request, status int, err error, step string) {
	slog.Error("error", "step", step, "err", err, "method", r.Method, "path", r.URL.Path)
	templ.Handler(
		xess.Base(
			"Oh noes!",
			headArea(),
			nil,
			ErrorWhy(step, fmt.Sprintf("%d: %v", status, err)),
			footer(),
		),
		templ.WithStatus(status),
	).ServeHTTP(w, r)
}

func (s *Server) POSTUpload(w http.ResponseWriter, r *http.Request) {
	fileBytes, err := ParseMultipartFile(r)
	if err != nil {
		s.Error(w, r, http.StatusBadRequest, err, "parse multipart file")
		return
	}

	img, _, err := image.Decode(bytes.NewReader(fileBytes))
	if err != nil {
		s.Error(w, r, http.StatusBadRequest, err, "decode image")
		return
	}

	dstImg := img
	if img.Bounds().Dx() > 800 {
		dstImg = imaging.Resize(img, 800, 0, imaging.Lanczos)
		slog.Info("resized image", "original_width", img.Bounds().Dx(), "new_width", dstImg.Bounds().Dx())
	}

	buf := new(bytes.Buffer)
	err = jpeg.Encode(buf, dstImg, nil)
	if err != nil {
		s.Error(w, r, http.StatusInternalServerError, err, "encode image as JPEG")
		return
	}

	shaSumOfImage := fmt.Sprintf("%x", sha256.Sum256(buf.Bytes()))
	slog.Info("sha256 sum of image", "sum", shaSumOfImage)

	if _, err := s.s3.HeadObject(r.Context(), &s3.HeadObjectInput{
		Bucket: bucketName,
		Key:    &shaSumOfImage,
	}); err != nil {
		if _, err := s.s3.PutObject(r.Context(), &s3.PutObjectInput{
			Body:         bytes.NewReader(buf.Bytes()),
			Bucket:       bucketName,
			Key:          &shaSumOfImage,
			ContentType:  aws.String("image/jpeg"),
			CacheControl: aws.String("public, max-age=31536000"),
		}); err != nil {
			s.Error(w, r, http.StatusInternalServerError, err, "upload image to s3")
			return
		}
		slog.Info("uploaded image to s3", "key", shaSumOfImage)
	} else {
		slog.Debug("image already exists", "key", shaSumOfImage)
	}

	resp, err := s.cli.Chat(r.Context(), &ollama.CompleteRequest{
		Model: *ollamaModel,
		Messages: []ollama.Message{
			{
				Content: "Explain how I would make this sandwich. Explain step by step in markdown. Do not include anything but your answer. Do not use code fences.",
				Role:    "user",
				Images:  [][]byte{buf.Bytes()},
			},
		},
	})
	if err != nil {
		s.Error(w, r, http.StatusInternalServerError, err, "generate instructions")
		return
	}

	instructions := mdToHTML([]byte(resp.Message.Content))

	ps := s3.NewPresignClient(s.s3)

	psReq, err := ps.PresignGetObject(r.Context(), &s3.GetObjectInput{
		Bucket: bucketName,
		Key:    &shaSumOfImage,
	}, func(opts *s3.PresignOptions) {
		opts.Expires = time.Duration(420 * time.Second)
	})
	if err != nil {
		s.Error(w, r, http.StatusInternalServerError, err, "presign image URL")
		return
	}

	templ.Handler(
		xess.Base(
			"How do I make this sandwich?",
			headArea(),
			nil,
			HowToMake(Unsafe(string(instructions)), psReq.URL),
			footer(),
		),
	).ServeHTTP(w, r)
}

func mdToHTML(md []byte) []byte {
	// create markdown parser with extensions
	extensions := parser.CommonExtensions | parser.AutoHeadingIDs | parser.NoEmptyLineBeforeBlock | parser.FencedCode | parser.Tables | parser.Strikethrough | parser.SpaceHeadings
	p := parser.NewWithExtensions(extensions)
	doc := p.Parse(md)

	// create HTML renderer with extensions
	htmlFlags := html.CommonFlags | html.HrefTargetBlank
	opts := html.RendererOptions{Flags: htmlFlags}
	renderer := html.NewRenderer(opts)

	return markdown.Render(doc, renderer)
}
