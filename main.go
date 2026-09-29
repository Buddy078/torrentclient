package main

import (
	"fmt"
	"log"
	"os"
	"strings"
)

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintf(os.Stderr, "usage: %s <torrent-file-or-magnet-link> <output-file-or-directory>\n", os.Args[0])
		os.Exit(1)
	}

	target := os.Args[1]
	outputPath := os.Args[2]

	var tf torrent_file
	var err error

	if strings.HasPrefix(target, "magnet:?") {
		magnet, err := parse_magnet_link(target)
		if err != nil {
			log.Fatalf("failed to parse magnet link: %v\n", err)
		}

		announceURL := ""
		if len(magnet.trackers) > 0 {
			announceURL = magnet.trackers[0]
		}

		tf = torrent_file{
			announce:  announceURL,
			info_hash: magnet.info_hash,
			name:      magnet.name,
		}

		fmt.Printf("Resolved magnet link: name=%s, info_hash=%x\n", tf.name, tf.info_hash)
		if tf.announce == "" {
			log.Fatalf("magnet link does not contain any tracker announce (tr) entries")
		}
	} else {
		tf, err = open(target)
		if err != nil {
			log.Fatalf("could not open torrent file: %v\n", err)
		}
	}

	if err := tf.download(outputPath); err != nil {
		log.Fatalf("download failed: %v\n", err)
	}

	fmt.Printf("Saved %s to %s\n", tf.name, outputPath)
}