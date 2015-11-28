package the_platinum_searcher

import "io"

type search struct {
	root    string
	pattern string
	out     io.Writer
	opts    Option
}

func (s search) start() {
	grepChan := make(chan string, 5000)
	done := make(chan struct{})

	go find{
		out:  grepChan,
		opts: s.opts,
	}.start(s.root)

	go grep{
		in:      grepChan,
		done:    done,
		printer: newPrinter(s.out, s.opts),
		opts:    s.opts,
	}.start(s.pattern)

	<-done
}
