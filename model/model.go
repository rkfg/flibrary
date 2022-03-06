package model

import "encoding/xml"

type Author struct {
	FirstName  string `xml:"first-name"`
	MiddleName string `xml:"middle-name"`
	LastName   string `xml:"last-name"`
	Nickname   string `xml:"nickname"`
}

type DocumentInfo struct {
	Author Author `xml:"author"`
}

type TitleInfo struct {
	Genre      string   `xml:"genre"`
	Author     Author   `xml:"author"`
	BookTitle  string   `xml:"book-title"`
	Annotation []string `xml:"annotation>p"`
	Date       string   `xml:"date"`
	Lang       string   `xml:"lang"`
}

type FB2 struct {
	XMLName   xml.Name  `xml:"FictionBook"`
	TitleInfo TitleInfo `xml:"description>title-info"`
}

type Book struct {
	ID           uint64
	Author       string
	AuthorLC     string `gorm:"index"`
	Title        string
	TitleLC      string `gorm:"index"`
	Genre        string `gorm:"index"`
	Annotation   string
	AnnotationLC string `gorm:"index"`
	Date         string `gorm:"index"`
	ZipFilename  string `gorm:"index:idx_zipfn"`
	Filename     string `gorm:"index:idx_zipfn;index:idx_fn"`
}
