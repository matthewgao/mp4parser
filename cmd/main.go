package main

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"os"
)

type Sample struct {
	Len  uint32
	Data []byte
}

func main() {
	in, err := os.OpenFile("./out.264", os.O_RDONLY, 777)
	if err != nil {
		fmt.Println(err.Error())
		return
	}

	allSample := []Sample{}
	for {
		buf := make([]byte, 4)
		n, err := io.ReadAtLeast(in, buf, 4)
		if err != nil {
			fmt.Printf("read len fail %d, %v\n", n, err)
			break
		}

		len := binary.BigEndian.Uint32(buf)
		fmt.Printf("got len %d, byte %x\n", len, buf)
		s := Sample{
			Len: len,
		}

		dataBuf := make([]byte, len)
		n, err = io.ReadAtLeast(in, dataBuf, int(len))
		if err != nil {
			fmt.Printf("read data fail %d, %v\n", n, err)
			break
		}

		s.Data = dataBuf
		allSample = append(allSample, s)
	}

	fmt.Printf("found %d frame\n", len(allSample))
	To264(allSample)
}

// 72491,
// 73293,

func To264(samples []Sample) {
	out, err := os.OpenFile("./raw.264", os.O_WRONLY, 777)
	if err != nil {
		fmt.Println(err.Error())
		return
	}
	defer out.Close()
	for _, v := range samples {
		// t := NalType(v.Data[0])
		startCode, _ := hex.DecodeString("00000001")
		// switch t {
		// case 5:

		// }
		out.Write(startCode)
		out.Write(v.Data)
	}
}

func NalType(b byte) int {
	t := b & 31
	return int(t)
}
