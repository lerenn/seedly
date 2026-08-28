package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
)

func main() {
	dataFile := flag.String("data", "", "file to torrent")
	out := flag.String("out", "sample.torrent", "output .torrent path")
	webseed := flag.String("webseed", "", "optional webseed base URL (file URL)")
	serve := flag.String("serve", "", "if set, serve data dir on this addr (e.g. :9090)")
	flag.Parse()
	if *dataFile == "" {
		log.Fatal("-data required")
	}

	info := metainfo.Info{PieceLength: 32 * 1024}
	if err := info.BuildFromFilePath(*dataFile); err != nil {
		log.Fatal(err)
	}

	mi := metainfo.MetaInfo{
		AnnounceList: [][]string{},
		CreatedBy:    "seedly-test",
		CreationDate: time.Now().Unix(),
	}
	if *webseed != "" {
		mi.UrlList = []string{*webseed}
	}
	var err error
	mi.InfoBytes, err = bencode.Marshal(&info)
	if err != nil {
		log.Fatal(err)
	}

	f, err := os.Create(*out)
	if err != nil {
		log.Fatal(err)
	}
	if err := mi.Write(f); err != nil {
		log.Fatal(err)
	}
	_ = f.Close()
	fmt.Printf("wrote %s infohash=%s name=%s\n", *out, mi.HashInfoBytes().HexString(), info.BestName())

	if *serve == "" {
		return
	}
	dir := filepath.Dir(*dataFile)
	fmt.Printf("serving %s on %s\n", dir, *serve)
	log.Fatal(http.ListenAndServe(*serve, http.FileServer(http.Dir(dir))))
}
