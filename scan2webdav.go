package main

import (
	"bytes"
	"io"
	"io/ioutil"
	"log"
	"mime/multipart"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"text/template"
	"time"

	"github.com/google/shlex"
	"github.com/kelseyhightower/envconfig"
	"github.com/rjeczalik/notify"
)

type Config struct {
	Server struct {
		Url  string `envconfig:"SERVER_URL"`
		User string `envconfig:"SERVER_USER"`
		Pass string `envconfig:"SERVER_PASS"`
	} `yaml:"server"`
	Watcher struct {
		Path string `envconfig:"WATCHER_PATH"`
	} `yaml:"watcher"`
	Ocr struct {
		Exec string `envconfig:"OCR_EXEC"`
		Args string `envconfig:"OCR_ARGS"`
	} `yaml:"ocr"`
}

func readEnv(cfg *Config) {
	err := envconfig.Process("", cfg)
	if err != nil {
		log.Fatal(err)
	}
}

func uploadFile(filename string, url string, user string, passwd string) *http.Response {
	buf := bytes.NewBuffer(nil)
	bodyWriter := multipart.NewWriter(buf)

	fileBase := filepath.Base(filename)
	url = url + "/" + fileBase

	fileWriter, err := bodyWriter.CreateFormFile("file", fileBase)
	if err != nil {
		log.Fatalf("Creating fileWriter: %s", err)
	}

	file, err := os.Open(filename)
	if err != nil {
		log.Fatalf("Opening file: %s", err)
	}
	defer file.Close()

	if _, err := io.Copy(fileWriter, file); err != nil {
		log.Fatalf("Buffering file: %s", err)
	}

	contentType := bodyWriter.FormDataContentType()

	// This is mandatory as it flushes the buffer.
	bodyWriter.Close()
	req, err := http.NewRequest(http.MethodPut, url, buf)
	if err != nil {
		log.Fatal(err)
	}
	req.SetBasicAuth(user, passwd)
	req.Header.Set("Content-Type", contentType)

	client := &http.Client{}
	res, err := client.Do(req)
	if err != nil {
		log.Println("Error uploading file:", err)
	}
	defer res.Body.Close()

	log.Println("Upload result for", filename, ":", res.Status)
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		bodyBytes, err := io.ReadAll(res.Body)
		if err != nil {
			log.Fatal(err)
		}
		bodyString := string(bodyBytes)
		log.Println(bodyString)
	}

	return (res)
}

func processFile(cfg Config, inFile string, wait bool) {
	log.Println("New file detected: " + inFile)
	// Wait 5 seconds to make sure file is complete
	if wait {
		time.Sleep(5 * time.Second)
	}

	log.Println("Processing file: " + inFile)

	// Create temp dir & file
	tempDir, err := ioutil.TempDir("/tmp", "ocrmypdf-*")
	if err != nil {
		log.Fatal(err)
	}
	defer os.RemoveAll(tempDir)
	log.Println("Temp direcotory created:", tempDir)

	tempFile := filepath.Join(tempDir, filepath.Base(inFile))

	// execute OCR
	args, err := shlex.Split(cfg.Ocr.Args)
	if err != nil {
		log.Printf("Error parsing arguments: %v\n", err)
	}
	args = append(args, inFile, tempFile)
	log.Println("Executing", cfg.Ocr.Exec, args)
	cmd := exec.Command(cfg.Ocr.Exec, args...)
	out, err := cmd.CombinedOutput()
	log.Println(string(out))

	if err != nil {
		log.Printf("Job failed: %v\n", err)

		// TODO: remember failed file to avoid reprocessing
	} else {
		log.Println("Job finished successfully.")

		res := uploadFile(tempFile, cfg.Server.Url, cfg.Server.User, cfg.Server.Pass)
		if res.StatusCode >= 200 && res.StatusCode < 300 {
			log.Println("Removing input:", inFile)
			os.Remove(inFile)
		} else {
			bodyBytes, err := io.ReadAll(res.Body)
			if err != nil {
				log.Println(err)
			}
			bodyString := string(bodyBytes)
			log.Println(bodyString)
		}
	}
	log.Println("Removing temp direcotory:", tempDir)
	os.RemoveAll(tempDir)
}

func processDir(cfg Config) {
	filepath.Walk(cfg.Watcher.Path, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			log.Println(err.Error())
		}
		if !info.IsDir() {
			processFile(cfg, cfg.Watcher.Path+"/"+info.Name(), false)
		}
		return nil
	})
}

func main() {
	var cfg Config
	// ocrmypdf defaults
	cfg.Ocr.Exec, _ = exec.LookPath("ocrmypdf")
	cfg.Ocr.Args = "--pdf-renderer sandwich --tesseract-timeout 1800 --rotate-pages -l eng+deu --deskew --clean --skip-text"
	readEnv(&cfg)

	// replace template patterns ( {{.User}} ) in URL
	t, err := template.New("url").Parse(cfg.Server.Url)
	if err != nil {
		log.Fatalln("Unable to parse url", err)
	}
	var tpl bytes.Buffer
	err = t.Execute(&tpl, cfg.Server)
	if err != nil {
		log.Fatalln("Unable to parse url", err)
	}
	cfg.Server.Url = tpl.String()
	log.Println("Upload-URL:", cfg.Server.Url)

	fileInfo, err := os.Stat(cfg.Watcher.Path)
	if err != nil {
		log.Fatalln("Unable to access watcher path", err)
	}

	if !fileInfo.IsDir() {
		log.Fatalln("Watcher path is not a directory", err)
	}

	// Process existing files first
	log.Println("Processing old files first")
	processDir(cfg)

	// Create new watcher.
	// Make the channel buffered to ensure no event is dropped. Notify will drop
	// an event if the receiver is not able to keep up the sending pace.
	c := make(chan notify.EventInfo, 1)

	// Set up a watchpoint
	log.Println("Watching " + cfg.Watcher.Path)
	if err := notify.Watch(cfg.Watcher.Path, c, notify.InCloseWrite, notify.InMovedTo); err != nil {
		log.Fatal(err)
	}
	defer notify.Stop(c)

	for {
		select {
		case ei := <-c:
			filename := ei.Path()
			go processFile(cfg, filename, true)
		}
	}
}
