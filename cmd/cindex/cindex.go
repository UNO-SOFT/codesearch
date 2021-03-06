// Copyright 2011, 2020 The Go Authors.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"runtime/pprof"
	"sort"
	"strings"

	"github.com/google/codesearch/index"
	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/htmlindex"
)

var usageMessage = `usage: cindex [-list] [-reset] [-encodings utf8,iso8859-2] [path...]

Cindex prepares the trigram index for use by csearch.  The index is the
file named by $CSEARCHINDEX, or else $HOME/.csearchindex.

The simplest invocation is

	cindex path...

which adds the file or directory tree named by each path to the index.
For example:

	cindex $HOME/src /usr/include

or, equivalently:

	cindex $HOME/src
	cindex /usr/include

If cindex is invoked with no paths, it reindexes the paths that have
already been added, in case the files have changed.  Thus, 'cindex' by
itself is a useful command to run in a nightly cron job.

The -list flag causes cindex to list the paths it has indexed and exit.

By default cindex adds the named paths to the index but preserves 
information about other paths that might already be indexed
(the ones printed by cindex -list).  The -reset flag causes cindex to
delete the existing index before indexing the new paths.
With no path arguments, cindex -reset removes the index.
`

func usage() {
	fmt.Fprintf(os.Stderr, usageMessage)
	os.Exit(2)
}

var (
	listFlag      = flag.Bool("list", false, "list indexed paths and exit")
	resetFlag     = flag.Bool("reset", false, "discard existing index")
	verboseFlag   = flag.Bool("verbose", false, "print extra information")
	cpuProfile    = flag.String("cpuprofile", "", "write cpu profile to this file")
	encodingsFlag = flag.String("encodings", "", "what encodings to use - comma separated list")
)

func main() {
	flag.Usage = usage
	flag.Parse()
	args := flag.Args()

	if *listFlag {
		ix := index.Open(index.File())
		for _, arg := range ix.Paths() {
			fmt.Printf("%s\n", arg)
		}
		return
	}

	if *cpuProfile != "" {
		f, err := os.Create(*cpuProfile)
		if err != nil {
			log.Fatal(err)
		}
		defer f.Close()
		pprof.StartCPUProfile(f)
		defer pprof.StopCPUProfile()
	}

	if *resetFlag && len(args) == 0 {
		os.Remove(index.File())
		return
	}
	if len(args) == 0 {
		ix := index.Open(index.File())
		for _, arg := range ix.Paths() {
			args = append(args, arg)
		}
	}

	// Translate paths to absolute paths so that we can
	// generate the file list in sorted order.
	for i, arg := range args {
		a, err := filepath.Abs(arg)
		if err != nil {
			log.Printf("%s: %s", arg, err)
			args[i] = ""
			continue
		}
		if s, err := os.Readlink(a); err == nil && s != "" {
			a = s
		}
		args[i] = a
	}
	sort.Strings(args)

	for len(args) > 0 && args[0] == "" {
		args = args[1:]
	}

	master := index.File()
	if _, err := os.Stat(master); err != nil {
		// Does not exist.
		*resetFlag = true
	}
	file := master
	if !*resetFlag {
		file += "~"
	}
	var encodings []encoding.Encoding
	for _, enc := range strings.Split(*encodingsFlag, ",") {
		if enc = strings.TrimSpace(enc); enc == "" {
			continue
		}
		e, err := htmlindex.Get(enc)
		if err != nil {
			log.Printf("%q: %+v", enc, err)
			return
		}
		encodings = append(encodings, e)
	}

	ix := index.Create(file)
	ix.Verbose = *verboseFlag
	ix.AddPaths(args)
	for _, arg := range args {
		log.Printf("index %s", arg)
		filepath.Walk(arg, func(path string, info os.FileInfo, err error) error {
			if _, elem := filepath.Split(path); elem != "" {
				// Skip various temporary or "hidden" files or directories.
				if elem[0] == '.' || elem[0] == '#' || elem[0] == '~' || elem[len(elem)-1] == '~' {
					if info.IsDir() {
						return filepath.SkipDir
					}
					return nil
				}
			}
			if err != nil {
				log.Printf("%s: %s", path, err)
				return nil
			}
			if info != nil && info.Mode()&os.ModeType == 0 {
				r, err := openEncoded(path, encodings)
				if err != nil {
					return fmt.Errorf("%q: %w", path, err)
				}
				ix.Add(path, r)
				r.Close()
			}
			return nil
		})
	}
	log.Printf("flush index")
	ix.Flush()

	if !*resetFlag {
		log.Printf("merge %s %s", master, file)
		index.Merge(file+"~", master, file)
		os.Remove(file)
		os.Rename(file+"~", master)
	}
	log.Printf("done")
	return
}

func openEncoded(path string, encodings []encoding.Encoding) (io.ReadCloser, error) {
	fh, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	var found encoding.Encoding
	for _, enc := range encodings {
		r := enc.NewDecoder().Reader(fh)
		if _, err = io.Copy(ioutil.Discard, r); err == nil {
			found = enc
		}
		if _, err = fh.Seek(0, 0); err != nil {
			return nil, err
		}
	}
	if found == nil {
		return fh, nil
	}
	return struct {
		io.Reader
		io.Closer
	}{found.NewDecoder().Reader(fh), fh}, nil
}
