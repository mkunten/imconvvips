package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
)

type config struct {
	DryRun      bool      `json:"-"`
	Verbose     bool      `json:"-"`
	Save        bool      `json:"-"`
	Proc        int       `json:"proc"`
	Type        string    `json:"type"`
	FilelistExt string    `json:"-"`
	SrcDir      string    `json:"src_dir"`
	DestDir     string    `json:"dest_dir"`
	ListDir     string    `json:"base_dir"`
	Ext         string    `json:"ext"`
	VipsFmt     string    `json:"vips_fmt"`
	LogName     string    `json:"log"`
	StdoutLog   string    `json:"stdout"`
	StderrLog   string    `json:"stderr"`
	Log         io.Writer `json:"-"`
	Stdout      io.Writer `json:"-"`
	Stderr      io.Writer `json:"-"`
}

var (
	confFile = "config.json"
	wg       sync.WaitGroup
)

func loadConfig() (*config, error) {
	// default settings:
	cfg := &config{
		Save:        false,
		DryRun:      false,
		Verbose:     false,
		Proc:        4,
		Type:        "files",
		FilelistExt: ".txt",
		SrcDir:      "src",
		DestDir:     "dest",
		ListDir:     "list",
		Ext:         ".jpg",
		VipsFmt:     "vips im_vips2tiff %s %s:jpeg:60,tile:256x256,pyramid",
		LogName:     "",
		StdoutLog:   "",
		StderrLog:   "",
		Log:         os.Stdout,
		Stdout:      os.Stdout,
		Stderr:      os.Stderr,
	}

	// load confFile if exists.
	f, err := os.Open(confFile)
	if err != nil {
		pathErr, ok := err.(*os.PathError)
		if !ok {
			return nil, err
		}
		if errno := pathErr.Err.(syscall.Errno); errno != syscall.ENOENT {
			return nil, err
		}
	} else {
		err = json.NewDecoder(f).Decode(&cfg)
		if err != nil {
			return nil, err
		}
	}
	f.Close()

	return cfg, nil
}

func saveConfig(cfg *config) error {
	f, err := os.Create(confFile)
	if err != nil {
		return err
	}
	defer f.Close()

	b, err := json.MarshalIndent(cfg, "", " ")
	if err != nil {
		return err
	}

	_, err = f.Write(b)
	if err != nil {
		return err
	}

	cfg.Log.Write([]byte(fmt.Sprintf("info: %s was successfully saved.\n",
		confFile)))
	return nil
}

func filesWalk(cfg *config, q chan string) error {
	return filepath.Walk(cfg.SrcDir,
		func(path string, info os.FileInfo, err error) error {
			if info.IsDir() {
				// if err := mkDestDir(cfg, path); err != nil {
				// 	return err
				// }
				return nil
			}
			q <- path
			return nil
		})
}

func filelistWalk(cfg *config, q chan string) error {
	return filepath.Walk(cfg.ListDir,
		func(path string, info os.FileInfo, err error) error {
			if filepath.Ext(path) != cfg.FilelistExt {
				// skip
				if cfg.Verbose {
					cfg.Log.Write([]byte(fmt.Sprintf("filelist skip (ext): %s\n",
						path)))
				}
				return nil
			}
			f, err := os.Open(path)
			if err != nil {
				return err
			}
			defer f.Close()

			if cfg.Verbose {
				cfg.Log.Write([]byte(fmt.Sprintf("filelist: %s\n", path)))
			}

			scanner := bufio.NewScanner(f)
			for scanner.Scan() {
				q <- filepath.Join(cfg.SrcDir, strings.TrimSpace(scanner.Text()))
			}
			if err = scanner.Err(); err != nil {
				return err
			}
			return nil
		})
}

func doVips(cfg *config, wg *sync.WaitGroup, q chan string) {
	defer wg.Done()
	for {
		src, ok := <-q
		if !ok {
			return
		}

		if filepath.Ext(src) != cfg.Ext {
			if cfg.Verbose {
				cfg.Log.Write([]byte(fmt.Sprintf("skip (ext): %s\n", src)))
			}
			continue
		}
		rel, err := filepath.Rel(cfg.SrcDir, src)
		if err != nil {
			cfg.Log.Write([]byte(fmt.Sprintf("error: %s\n", err)))
			continue
		}
		dest := filepath.Join(cfg.DestDir, rel)
		if cfg.Ext != ".jpg" {
			dest = dest[0:len(dest)-4] + ".jpg"
		}

		if cfg.Verbose {
			cfg.Log.Write([]byte(fmt.Sprintf("%s -> %s\n", src, dest)))
		}
		if !cfg.DryRun {
			os.MkdirAll(filepath.Dir(dest), 0755)

			s := fmt.Sprintf(cfg.VipsFmt, src, dest)
			cmd := exec.Command("sh", "-c", s)
			cmd.Stdout = cfg.Stdout
			cmd.Stderr = cfg.Stderr
			if err := cmd.Run(); err != nil {
				cfg.Log.Write([]byte(fmt.Sprintf("error: %s:\n  %s\n", s, err)))
			}
		}
	}
}

func exitOnError(err error) {
	fmt.Println(err)
	os.Exit(1)
}

func main() {
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: %s [options]\n\nOptions:\n", os.Args[0])
		flag.PrintDefaults()
		fmt.Fprintf(os.Stderr,
			"\n  *default values have been changed via config.json if exists.\n")
	}

	cfg, err := loadConfig()
	if err != nil {
		exitOnError(err)
	}

	// update by commandline options.
	flag.BoolVar(&cfg.DryRun, "t", cfg.DryRun, "dry run (test)")
	flag.BoolVar(&cfg.Verbose, "v", cfg.DryRun, "verbose")
	flag.BoolVar(&cfg.Save, "save", cfg.Save,
		"overwrite config.json with current config")
	flag.IntVar(&cfg.Proc, "p", cfg.Proc, "concurrent processes")
	flag.StringVar(&cfg.Type, "type", cfg.Type,
		"type (\"files\" or \"filelist[.{ext}]\")")
	flag.StringVar(&cfg.SrcDir, "s", cfg.SrcDir,
		"source dir (absolutive/relative)")
	flag.StringVar(&cfg.DestDir, "d", cfg.DestDir,
		"destination dir (absolutive/relative)")
	flag.StringVar(&cfg.ListDir, "b", cfg.ListDir,
		"filelist dir (absolutive/relative)")
	flag.StringVar(&cfg.Ext, "e", cfg.Ext, "source file extention")
	flag.StringVar(&cfg.VipsFmt, "f", cfg.VipsFmt,
		"vips command format for fmt.Sprintf with two args "+
			"(src filename, dest filename)")
	flag.StringVar(&cfg.LogName, "log", cfg.LogName,
		"log file name (\"\" to use stdout)")
	flag.StringVar(&cfg.StdoutLog, "stdout", cfg.StdoutLog,
		"stdout logfile of vips (\"\" to use stdout)")
	flag.StringVar(&cfg.StderrLog, "stderr", cfg.StderrLog,
		"stderr logfile of vips (\"\" to use stderr)")
	flag.Parse()

	// after parsing args
	if cfg.DryRun {
		cfg.Verbose = true
	}
	if cfg.LogName != "" {
		logfile, err := os.Create(cfg.LogName)
		if err != nil {
			exitOnError(err)
		}
		defer logfile.Close()
		cfg.Log = io.MultiWriter(os.Stdout, logfile)
	} else {
		cfg.Log = os.Stdout
	}
	if cfg.StdoutLog != "" {
		cfg.Stdout, err = os.Create(cfg.StdoutLog)
		if err != nil {
			exitOnError(err)
		}
		defer cfg.Stdout.(*os.File).Close()
	}
	if cfg.StderrLog != "" {
		cfg.Stderr, err = os.Create(cfg.StderrLog)
		if err != nil {
			exitOnError(err)
		}
		defer cfg.Stderr.(*os.File).Close()
	}
	if cfg.Type != "files" {
		if cfg.Type[0:8] != "filelist" {
			exitOnError(errors.New(
				"type must be \"files\" or \"filelist[.{ext}]\""))
		} else {
			cfg.FilelistExt = cfg.Type[8:]
		}
	}

	// save conf if necessary.
	if cfg.Save {
		if err = saveConfig(cfg); err != nil {
			exitOnError(err)
		}
	}

	// config normalization without saving
	cfg.SrcDir = filepath.FromSlash(cfg.SrcDir)
	cfg.DestDir = filepath.FromSlash(cfg.DestDir)
	cfg.ListDir = filepath.FromSlash(cfg.ListDir)

	if cfg.Verbose {
		cfg.Log.Write([]byte(fmt.Sprintf("config: %#v\n", cfg)))
	}

	// prepare workers
	q := make(chan string)
	wg.Add(cfg.Proc)
	for i := 0; i < cfg.Proc; i++ {
		go doVips(cfg, &wg, q)
	}

	// do queuing
	if cfg.Type == "files" {
		if err = filesWalk(cfg, q); err != nil {
			cfg.Log.Write([]byte(fmt.Sprintf("error: %s\n", err)))
		}
	} else {
		if err = filelistWalk(cfg, q); err != nil {
			cfg.Log.Write([]byte(fmt.Sprintf("error: %s\n", err)))
		}
	}

	close(q)
	wg.Wait()

	fmt.Println("done!")
}
