package main

import (
	"archive/zip"
	"encoding/xml"
	"io"
	"io/fs"
	"log"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/jessevdk/go-flags"
	"golang.org/x/net/html/charset"
	"golang.org/x/text/encoding/unicode"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"rkfg.me/flibrary/model"
)

type task struct {
	zipfilename string
	zf          *zip.File
}

var infoTags = map[int]map[string]bool{
	0: {
		"FictionBook": false,
	},
	1: {
		"description": true,
	},
	2: {
		"title-info": false,
	},
	3: {
		"genre":      false,
		"author":     false,
		"book-title": false,
		"annotation": false,
		"date":       false,
		"lang":       false,
	},
	4: {
		"first-name":  false,
		"middle-name": false,
		"last-name":   false,
		"nickname":    false,
	},
}

var params struct {
	Database string `short:"d" long:"database" description:"Path to the database" required:"true"`
}

func parseXML(dec *xml.Decoder) (result model.FB2, err error) {
	depth := 0
	inElement := ""
	for {
		var tok xml.Token
		tok, err = dec.Token()
		if err != nil {
			if err == io.EOF {
				return result, nil
			}
			return
		}
		switch tt := tok.(type) {
		case xml.StartElement:
			if _, ok := infoTags[depth][tt.Name.Local]; ok {
				inElement = tt.Name.Local
				depth++
			}
		case xml.CharData:
			if inElement == "" {
				continue
			}
			text := string(tt)
			switch inElement {
			case "genre":
				result.TitleInfo.Genre = text
			case "first-name":
				result.TitleInfo.Author.FirstName = text
			case "middle-name":
				result.TitleInfo.Author.MiddleName = text
			case "last-name":
				result.TitleInfo.Author.LastName = text
			case "nickname":
				result.TitleInfo.Author.Nickname = text
			case "book-title":
				result.TitleInfo.BookTitle = text
			case "date":
				result.TitleInfo.Date = text
			case "lang":
				result.TitleInfo.Lang = text
			case "annotation":
				text = strings.Trim(text, " \n")
				if len(text) > 0 {
					result.TitleInfo.Annotation = append(result.TitleInfo.Annotation, text)
				}
			}
		case xml.EndElement:
			if end, ok := infoTags[depth-1][tt.Name.Local]; ok {
				depth--
				if end {
					return
				}
				inElement = ""
			}
		}
	}
}

func parse(wg *sync.WaitGroup, taskchan chan task, bookchan chan model.Book) {
	defer wg.Done()
	for task := range taskchan {
		contents, err := task.zf.Open()
		if err != nil {
			log.Print(err)
			continue
		}
		dec := xml.NewDecoder(contents)
		dec.CharsetReader = charset.NewReaderLabel
		dec.Strict = false
		dec.AutoClose = append(xml.HTMLAutoClose, "p")
		fb2, err := parseXML(dec)
		contents.Close()
		if err != nil {
			origErr := err
			contents, _ = task.zf.Open()
			utf16contents := unicode.UTF16(unicode.LittleEndian, unicode.UseBOM).NewDecoder().Reader(contents)
			dec := xml.NewDecoder(utf16contents)
			dec.CharsetReader = charset.NewReaderLabel
			fb2, err = parseXML(dec)
			contents.Close()
			if err != nil {
				log.Printf("Couldn't decode %s/%s: %s (previously: %s)", task.zipfilename, task.zf.Name, err, origErr)
				continue
			}
		}
		book := model.Book{
			Title:      strings.TrimSpace(fb2.TitleInfo.BookTitle),
			Date:       strings.TrimSpace(fb2.TitleInfo.Date),
			Genre:      strings.TrimSpace(fb2.TitleInfo.Genre),
			Annotation: strings.Join(fb2.TitleInfo.Annotation, " "),
		}
		authorArr := []string{}
		if fb2.TitleInfo.Author.FirstName != "" {
			authorArr = append(authorArr, strings.TrimSpace(fb2.TitleInfo.Author.FirstName))
		}
		if fb2.TitleInfo.Author.MiddleName != "" {
			authorArr = append(authorArr, strings.TrimSpace(fb2.TitleInfo.Author.MiddleName))
		}
		if fb2.TitleInfo.Author.LastName != "" {
			authorArr = append(authorArr, strings.TrimSpace(fb2.TitleInfo.Author.LastName))
		}
		book.Author = strings.Join(authorArr, " ")
		book.AuthorLC = strings.ToLower(book.Author)
		book.TitleLC = strings.ToLower(book.Title)
		book.AnnotationLC = strings.ToLower(book.Annotation)
		book.Filename = filepath.Base(task.zf.Name)
		book.ZipFilename = task.zipfilename
		bookchan <- book
	}
}

func walkDir(root string) chan string {
	zips := make(chan string, 100)
	go func() {
		filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				log.Printf("Error scanning %s: %s", path, err)
				return nil
			}
			if !d.IsDir() && filepath.Ext(d.Name()) == ".zip" {
				zips <- path
			}
			return nil
		})
		close(zips)
	}()
	return zips
}

func parseZip(wg *sync.WaitGroup, zipchan chan string, db *gorm.DB) chan task {
	taskchan := make(chan task, 100)
	go func() {
		defer wg.Done()
		for zf := range zipchan {
			r, err := zip.OpenReader(zf)
			if err != nil {
				log.Println(err)
				continue
			}
			log.Printf("Processing %s", zf)
			for _, f := range r.File {
				if f.FileInfo().IsDir() {
					continue
				}
				if filepath.Ext(f.Name) != ".fb2" {
					continue
				}
				t := task{zipfilename: filepath.Base(zf), zf: f}
				var c int64
				db.Model(&model.Book{}).Where("zip_filename = ? AND filename = ?", t.zipfilename, t.zf.Name).Count(&c)
				if c == 0 {
					taskchan <- t
				}
			}
		}
		close(taskchan)
	}()
	return taskchan
}

func storeToDB(wg *sync.WaitGroup, db *gorm.DB, bookchan chan model.Book) {
	defer wg.Done()
	cnt := int64(0)
	start := time.Now()
	last := start
	for book := range bookchan {
		db.Save(&book)
		cnt++
		if time.Since(last).Seconds() > 5 {
			log.Printf("%d processed, speed: %d files/s", cnt, cnt/int64(time.Since(start).Seconds()))
			last = last.Add(5 * time.Second)
		}
	}
}

func main() {
	path, err := flags.Parse(&params)
	if err != nil {
		return
	}
	if len(path) < 1 {
		log.Fatal("No path given")
	}
	db, err := gorm.Open(sqlite.Open(params.Database))
	if err != nil {
		log.Fatal(err)
	}
	db.AutoMigrate(&model.Book{})
	var wgparsers, wgmisc sync.WaitGroup
	zipchan := walkDir(path[0])
	taskchan := parseZip(&wgmisc, zipchan, db)
	bookchan := make(chan model.Book, 100)
	for i := 0; i < runtime.NumCPU(); i++ {
		wgparsers.Add(1)
		go parse(&wgparsers, taskchan, bookchan)
	}
	wgmisc.Add(2)
	go storeToDB(&wgmisc, db, bookchan)
	wgparsers.Wait()
	close(bookchan)
	wgmisc.Wait()
}
