package main

import (
	"CgbiPngFix/ipaPng"
	"bytes"
	"flag"
	"fmt"
	"image/png"
	"io/ioutil"
	"log"
	"os"
)

type CommandOptions struct {
	Output string
	Input string

}

var ShowHelper bool
var Options CommandOptions

func init() {

	flag.BoolVar(&ShowHelper, "h", false, "show this help")

	// 注意 `signal`。默认是 -s string，有了 `signal` 之后，变为 -s signal
	flag.StringVar(&Options.Output, "o", "", "set fixed png `output` file")
	flag.StringVar(&Options.Input, "i", "", "set source ios png `input` file")

	// 改变默认的 Usage，flag包中的Usage 其实是一个函数类型。这里是覆盖默认函数实现，具体见后面Usage部分的分析
	flag.Usage = usage
}


func usage() {
	fmt.Fprintf(os.Stderr, `ios png fix version: v0.0.1
Usage: nginx [-h] [-o filename] [-i filename]

Options:
`)
	flag.PrintDefaults()
}

func main() {
	flag.Parse()

	if ShowHelper {
		flag.Usage()
		os.Exit(0)
	}
	if Options.Input == "" {
		flag.Usage()
		os.Exit(0)
	}
	doCgbiToPng(Options.Input, Options.Output)
}

func doCgbiToPng(input string, output string) {
	f, err := os.Open(input)
	if err != nil {
		log.Fatal(err)
	}
	b, err := ioutil.ReadAll(f)
	f.Close()
	if err != nil {
		log.Fatal(err)
	}

	cgbi, err := ipaPng.Decode(bytes.NewReader(b))
	if err != nil {
		fmt.Printf("err:%v\n", err)
		log.Fatal(err)
	}
	fo, err := os.OpenFile(output, os.O_WRONLY|os.O_CREATE, 666)
	if err != nil {
		fmt.Printf("err:%v\n", err)
		log.Fatal(err)
	}
	defer fo.Close()
	err = png.Encode(fo, cgbi.Img)
	if err != nil {
		fmt.Printf("err:%v\n", err)
		log.Fatal(err)
	}
}