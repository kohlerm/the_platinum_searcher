package the_platinum_searcher

import (
	"bufio"
	"bytes"
	"io"
	"io/ioutil"
	"log"
	"os"
	"sync"

	"golang.org/x/text/encoding/japanese"
	"golang.org/x/text/transform"
)

var newLine = []byte("\n")

type grep struct {
	in      chan string
	done    chan struct{}
	printer printer
	opts    Option
}

func (g grep) start(pattern string) {
	sem := make(chan struct{}, 208)
	wg := &sync.WaitGroup{}

	grepper := newGrepper(g.printer, opts)

	p := newPattern(pattern, opts.SearchOption.Regexp)

	for path := range g.in {
		sem <- struct{}{}
		wg.Add(1)
		go grepper.grep(path, p, sem, wg)
	}
	wg.Wait()
	g.done <- struct{}{}
}

type grepper interface {
	grep(path string, pattern pattern, sem chan struct{}, wg *sync.WaitGroup)
}

func newGrepper(printer printer, opts Option) grepper {
	if opts.SearchOption.Regexp {
		return extendedGrep{printer}
	} else {
		return fixedGrep{printer}
	}
}

type fixedGrep struct {
	printer printer
}

func (g fixedGrep) grep(path string, p pattern, sem chan struct{}, wg *sync.WaitGroup) {
	f, err := os.Open(path)
	if err != nil {
		log.Fatalf("open: %s\n", err)
	}

	defer func() {
		f.Close()
		<-sem
		wg.Done()
	}()

	buf := make([]byte, 8196)
	var stash []byte
	identified := false
	var encoding int
	pattern := []byte(p.pattern)

	for {
		c, err := f.Read(buf)
		if err != nil && err != io.EOF {
			log.Fatalf("read: %s\n", err)
		}

		if err == io.EOF {
			break
		}

		// detect encoding.
		if !identified {
			pattern, encoding = encodedPattern(c, buf, pattern)
			if pattern == nil {
				break
			}
			identified = true
		}

		// repair first line from previous last line.
		if len(stash) > 0 {
			var repaired []byte
			index := bytes.Index(buf[:c], newLine)
			if index == -1 {
				repaired = append(stash, buf[:c]...)
			} else {
				repaired = append(stash, buf[:index]...)
			}
			// grep from repaied line.
			if bytes.Contains(repaired, pattern) {
				g.grepAsLines(f, pattern, encoding)
				break
			}
		}

		// grep from buffer.
		if bytes.Contains(buf[:c], pattern) {
			g.grepAsLines(f, pattern, encoding)
			break
		}

		// stash last line.
		index := bytes.LastIndex(buf[:c], newLine)
		if index == -1 {
			stash = append(stash, buf[:c]...)
		} else {
			stash = make([]byte, c-index)
			copy(stash, buf[index:c])
		}
	}
}

func (g fixedGrep) grepAsLines(f *os.File, pattern []byte, encoding int) {
	f.Seek(0, 0)
	match := match{path: f.Name(), pattern: pattern, encoding: encoding}
	scanner := bufio.NewScanner(f)
	line := 1
	for scanner.Scan() {
		if bytes.Contains(scanner.Bytes(), pattern) {
			match.add(line, scanner.Text())
		}
		line++
	}
	g.printer.print(match)
}

type extendedGrep struct {
	printer printer
}

func (g extendedGrep) grep(path string, p pattern, sem chan struct{}, wg *sync.WaitGroup) {
	f, err := os.Open(path)
	if err != nil {
		log.Fatalf("open: %s\n", err)
	}

	defer func() {
		f.Close()
		<-sem
		wg.Done()
	}()

	buf := make([]byte, 512)

	c, err := f.Read(buf)
	if err != nil && err != io.EOF {
		log.Fatalf("read: %s\n", err)
	}

	if err == io.EOF {
		return
	}

	// detect encoding.
	pattern, encoding := encodedPattern(c, buf, []byte(p.pattern))
	if pattern == nil {
		return
	}
	p = newPattern(string(pattern), true)

	f.Seek(0, 0)

	match := match{path: f.Name(), regexp: p.regexp, encoding: encoding}
	scanner := bufio.NewScanner(f)
	line := 1
	for scanner.Scan() {
		if p.regexp.Match(scanner.Bytes()) {
			match.add(line, scanner.Text())
		}
		line++
	}
	if match.size() > 0 {
		g.printer.print(match)
	}
}

func encodedPattern(c int, buf []byte, pattern []byte) ([]byte, int) {
	limit := c
	if limit > 512 {
		limit = 512
	}
	encoding := detectEncoding(buf[:limit])
	if encoding == ERROR || encoding == BINARY {
		return nil, encoding
	}

	if encoder := getEncoder(encoding); encoder != nil {
		// encode pattern to shift-jis or euc-jp.
		encoded, _ := ioutil.ReadAll(transform.NewReader(bytes.NewReader(pattern), encoder))
		return encoded, encoding
	}
	return pattern, encoding
}

func getEncoder(encoding int) transform.Transformer {
	switch encoding {
	case EUCJP:
		return japanese.EUCJP.NewEncoder()
	case SHIFTJIS:
		return japanese.ShiftJIS.NewEncoder()
	}
	return nil
}

// 1. grepにencoderとdecoderを保持する
// 2. encoderはファイルの文字コードに従い、patternを変換する
// 3. decoderはパターン検索で合致した文字列をファイルの文字コードからdecodeする
// 4. printerは文字コード変換前のpatternを最初から保持しておき、置換に利用する(3.でdecode済みの文字列が返ってくるため)
// grepは内部的に文字コードを変換するだけで最終的な出力はUTF-8で返すようにする
