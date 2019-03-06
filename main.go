package main

import (
	"encoding/binary"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/matthewgao/mp4reader/mp4"
)

var inputFile string
var f mp4.File

func init() {
	flag.StringVar(&inputFile, "i", "", "-i input_file.mp4")
	flag.Parse()
}

func main() {
	if inputFile == "" {
		flag.Usage()
		return
	}
	f, err := mp4.Open(inputFile)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return
	}
	defer f.Close()

	f.PrintInfo()
	chunks := f.GetChunk(0)
	samples := f.GetSample(0)
	out, err := os.OpenFile("./out.264", os.O_WRONLY, 777)
	if err != nil {
		fmt.Println(err.Error())
		return
	}
	for k := range chunks {
		offset := chunks[k].GetOffset()
		ss := chunks[k].GetStartSample()
		s := samples[ss-1]

		f.Seek(int64(offset), 0)

		// c, _ := io.Copy(out, f)
		// fmt.Println(c)

		Save(f.File, out, int64(s.GetSize()))
	}
	// fmt.Println(err)

	out.Close()
}

type Sample struct {
	Len  uint32
	Data []byte
}

func Save(in *os.File, out *os.File, max int64) {

	allSample := []Sample{}
	for {
		buf := make([]byte, 4)
		n, err := io.ReadAtLeast(in, buf, 4)
		if err != nil {
			fmt.Printf("read len fail %d, %v\n", n, err)
			return
		}
		max = max - int64(n)

		len := binary.BigEndian.Uint32(buf)
		if len > uint32(max) {
			fmt.Printf("reset len %d, max %d\n", len, max)
			len = uint32(max)
		}
		fmt.Printf("got len %d, max %d, byte %x\n", len, max, buf)
		s := Sample{
			Len: len,
		}

		dataBuf := make([]byte, len)

		n, err = io.ReadAtLeast(in, dataBuf, int(len))
		if err != nil {
			fmt.Printf("read data fail %d, %v\n", n, err)
			return
		}

		s.Data = dataBuf
		allSample = append(allSample, s)
		max = max - int64(n)
		if max <= 0 {
			break
		}
	}

	fmt.Printf("found frame %d\n", len(allSample))
	To264(allSample, out)
}

// 72491,
// 73293,

func To264(samples []Sample, out *os.File) {
	// out, err := os.OpenFile("./raw.264", os.O_WRONLY, 777)
	// if err != nil {
	// 	fmt.Println(err.Error())
	// 	return
	// }
	// defer out.Close()
	for _, v := range samples {
		t := NalType(v.Data[0])
		startCode, _ := hex.DecodeString("00000001")
		// switch t {
		// case 5:

		fmt.Printf("------NAL type %d\n", t)
		// if t == 5 || t == 7 || t == 8 {
		out.Write(startCode)
		out.Write(v.Data)
		// }
	}
}

func NalType(b byte) int {
	t := b & 31
	return int(t)
}
