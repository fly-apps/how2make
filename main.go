package main

import (
	"context"
	"flag"
	"io"
	"log"
	"log/slog"
	"net/http"

	"github.com/a-h/templ"
	"github.com/facebookgo/flagenv"
	_ "github.com/joho/godotenv/autoload"
	"within.website/x/tigris"
	"within.website/x/web/ollama"
	"within.website/x/xess"

	// image formats
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"

	// more image formats
	_ "github.com/gen2brain/avif"
	_ "github.com/gen2brain/heic"
	_ "github.com/gen2brain/jpegxl"
	_ "github.com/gen2brain/webp"
	_ "golang.org/x/image/bmp"
	_ "golang.org/x/image/tiff"
	_ "golang.org/x/image/vp8"
	_ "golang.org/x/image/vp8l"
)

//go:generate go run github.com/a-h/templ/cmd/templ@latest generate

var (
	bind        = flag.String("bind", "localhost:8080", "address to bind to")
	bucketName  = flag.String("bucket", "how2make-uploads", "s3 bucket name")
	ollamaHost  = flag.String("ollama-host", "http://gpu-recipeficator.flycast", "ollama host")
	ollamaModel = flag.String("ollama-model", "llava", "ollama model")
)

func main() {
	flagenv.Parse()
	flag.Parse()

	cli := ollama.NewClient(*ollamaHost)
	_ = cli

	mux := http.NewServeMux()
	xess.Mount(mux)

	s3, err := tigris.Client(context.Background())
	if err != nil {
		log.Fatal(err)
	}

	s := &Server{
		cli: cli,
		s3:  s3,
	}

	mux.Handle("GET /{$}", templ.Handler(
		xess.Base(
			"How do I make this sandwich?",
			headArea(),
			nil,
			index(),
			footer(),
		),
	))
	mux.HandleFunc("/", s.NotFound)
	mux.HandleFunc("POST /upload", s.POSTUpload)

	slog.Info("server", "listening", *bind)
	log.Fatal(http.ListenAndServe(*bind, mux))
}

func Unsafe(html string) templ.Component {
	return templ.ComponentFunc(func(ctx context.Context, w io.Writer) (err error) {
		_, err = io.WriteString(w, html)
		return
	})
}
