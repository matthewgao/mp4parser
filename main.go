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

	// f.PrintInfo()
	out, err := os.OpenFile("./out.264", os.O_WRONLY, 777)
	if err != nil {
		fmt.Println(err.Error())
		return
	}
	defer out.Close()

	samples := make(chan mp4.SampleStruct, 32)
	stop := make(chan bool, 0)
	go GetAllSample(f, samples, stop)
	// hasPpsSps := FoundPpsSps(samples)
	// fmt.Printf("has sps pps %v\n", hasPpsSps)
	// if !hasPpsSps {
	avcc := GetAVCC(f)
	// ppsSample := []mp4.SampleStruct{}
	// //must sps first, pps second
	// ppsSample = append(ppsSample, mp4.SampleStruct{
	// 	Len:  uint32(avcc.SpsLen),
	// 	Data: avcc.Sps,
	// }, mp4.SampleStruct{
	// 	Len:  uint32(avcc.PpsLen),
	// 	Data: avcc.Pps,
	// })
	// samples = append(ppsSample, samples...)
	// // }

	samples <- mp4.SampleStruct{
		Len:  uint32(avcc.SpsLen),
		Data: avcc.Sps,
	}

	samples <- mp4.SampleStruct{
		Len:  uint32(avcc.PpsLen),
		Data: avcc.Pps,
	}

	SampleTo264(samples, stop, out)
}

func GetAVCC(f *mp4.File) AVCC {
	stsd := f.Moov.Traks[0].Mdia.Minf.Stbl.Stsd
	fmt.Printf("%x\n", stsd.Other_data)
	fmt.Printf("entry %d\n", stsd.Entry_count)

	// for index := 0; uint32(index) < stsd.Entry_count; index++ {
	avcc := ReadAVCC(&stsd.Other_data, 0)
	// }

	return avcc
}

type AVC1 struct {
	Size    uint32
	BoxType []byte
	Height  uint16
	Width   uint16
}

func ReadAVCC(data *[]byte, offset int) AVCC {

	w := (*data)[32:34]
	h := (*data)[34:36]
	s := (*data)[0:4]
	fmt.Printf("h %x, w %x\n", h, w)

	avc1 := AVC1{
		Size:    binary.BigEndian.Uint32(s),
		BoxType: (*data)[4:8],
		Height:  binary.BigEndian.Uint16(h),
		Width:   binary.BigEndian.Uint16(w),
	}

	fmt.Printf("h %d, w %d\n", avc1.Height, avc1.Width)
	fmt.Printf("type %s, size: %d\n", string(avc1.BoxType), avc1.Size)

	s1 := (*data)[86:90]
	avcc := AVCC{
		Size:    binary.BigEndian.Uint32(s1),
		BoxType: (*data)[90:94],
		SpsNum:  int8((*data)[99] & 31),
		SpsLen:  binary.BigEndian.Uint16((*data)[100:102]),
	}

	avcc.Sps = (*data)[102 : 102+avcc.SpsLen]
	avcc.PpsNum = int8((*data)[102+avcc.SpsLen] & 31)
	avcc.PpsLen = binary.BigEndian.Uint16((*data)[102+avcc.SpsLen+1 : 102+avcc.SpsLen+3])
	avcc.Pps = (*data)[102+avcc.SpsLen+3 : 102+avcc.SpsLen+3+avcc.PpsLen]
	//111 0 0000
	fmt.Printf("type %s, size: %d\n", string(avcc.BoxType), avcc.Size)
	fmt.Printf("spsNum %d, spslen %d\n", avcc.SpsNum, avcc.SpsLen)
	fmt.Printf("ppsNum %d, ppslen %d\n", avcc.PpsNum, avcc.PpsLen)

	return avcc
}

type AVCC struct {
	Size    uint32
	BoxType []byte
	SpsNum  int8
	SpsLen  uint16
	Sps     []byte
	PpsNum  int8
	PpsLen  uint16
	Pps     []byte
}

func GetSPS(f *mp4.File) []byte {
	return nil
}

func GetAllSample(f *mp4.File, out chan mp4.SampleStruct, stop chan bool) {
	chunks := f.GetChunk(0)
	samples := f.GetSample(0)
	for k := range chunks {
		offset := chunks[k].GetOffset()
		ss := chunks[k].GetStartSample()
		s := samples[ss-1]

		f.Seek(int64(offset), 0)
		sample := ExtractSample(f.File, int64(s.GetSize()))
		// fmt.Printf("found frame %d\n", len(sample))
		for _, v := range sample {
			out <- v
		}
	}

	stop <- true
}

func ExtractSample(in *os.File, size int64) []mp4.SampleStruct {
	allSample := []mp4.SampleStruct{}
	for {
		buf := make([]byte, 4)
		n, err := io.ReadAtLeast(in, buf, 4)
		if err != nil {
			fmt.Printf("read len fail %d, %v\n", n, err)
			return allSample
		}
		size = size - int64(n)

		len := binary.BigEndian.Uint32(buf)
		if len > uint32(size) {
			fmt.Printf("reset len %d, size %d\n", len, size)
			len = uint32(size)
		}
		// fmt.Printf("got len %d, size %d, byte %x\n", len, size, buf)
		s := mp4.SampleStruct{
			Len: len,
		}

		dataBuf := make([]byte, len)

		n, err = io.ReadAtLeast(in, dataBuf, int(len))
		if err != nil {
			fmt.Printf("read data fail %d, %v\n", n, err)
			return allSample
		}

		s.Data = dataBuf
		allSample = append(allSample, s)
		size = size - int64(n)
		if size <= 0 {
			break
		}
	}
	return allSample
}

func HasSPS(data *[]byte) bool {
	t := NalType((*data)[0])
	if t == 7 {
		return true
	}
	return false
}

func HasPPS(data *[]byte) bool {
	t := NalType((*data)[0])
	if t == 8 {
		return true
	}
	return false
}

func FoundPpsSps(samples []mp4.SampleStruct) bool {
	pps := false
	sps := false
	for _, v := range samples {
		if !pps && HasPPS(&(v.Data)) {
			pps = true
		}

		if !sps && HasSPS(&(v.Data)) {
			sps = true
		}

		if pps && sps {
			return true
		}
	}

	return false
}

func SampleTo264(samples chan mp4.SampleStruct, stop chan bool, out *os.File) {
	for {
		select {
		case sample := <-samples:
			t := NalType(sample.Data[0])
			startCode, _ := hex.DecodeString("00000001")
			// switch t {
			// case 5:

			fmt.Printf("------NAL type %d\n", t)
			// if t == 5 || t == 7 || t == 8 {
			out.Write(startCode)
			out.Write(sample.Data)
		// }
		case stop := <-stop:
			if stop {
				return
			}
		}
	}
	// for _, v := range samples {
	// 	// t := NalType(v.Data[0])
	// 	startCode, _ := hex.DecodeString("00000001")
	// 	// switch t {
	// 	// case 5:

	// 	// fmt.Printf("------NAL type %d\n", t)
	// 	// if t == 5 || t == 7 || t == 8 {
	// 	out.Write(startCode)
	// 	out.Write(v.Data)
	// 	// }
	// }
}

func NalType(b byte) int {
	t := b & 31
	return int(t)
}
