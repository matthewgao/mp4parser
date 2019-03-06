package mp4

import (
	"encoding/binary"
	"fmt"
	"os"
)

const (
	BOX_HEADER_SIZE = int64(8)
)

func Open(path string) (f *File, err error) {
	// fmt.Println(flag.Args())
	fmt.Println(path)

	file, err := os.OpenFile(path, os.O_RDONLY, 0400)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return nil, err
	}

	f = &File{
		File: file,
	}

	return f, f.parse()
}

func (f *File) parse() error {
	info, err := f.Stat()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return err
	}
	fmt.Printf("File size: %v \n", info.Size)
	f.Size = info.Size()

	// Loop through top-level Boxes
	boxes := readBoxes(f, int64(0), f.Size)
	for box := range boxes {
		switch box.Name {
		case "ftyp":
			f.Ftyp = &FtypBox{Box: box}
			f.Ftyp.parse()
		case "moov":
			f.Moov = &MoovBox{Box: box}
			f.Moov.parse()
		case "mdat":
			f.Mdat = box
		default:
			fmt.Printf("Unhandled Box: %v \n", box.Name)
		}
	}

	// Make sure we have all 3 required boxes
	if f.Ftyp == nil || f.Moov == nil || f.Mdat == nil {
		return fmt.Errorf("Missing a required box (ftyp, moov, or mdat)")
	}

	// Build chunk & sample tables
	fmt.Println("Building trak tables...")
	if err = f.buildTrakTables(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return err
	}
	fmt.Println("Chunk and Sample tables built.")

	return nil
}

func (f *File) PrintInfo() {
	for _, v := range f.Moov.GetTraks() {
		v.PrintChunk()
		v.PrintSample()
	}
}

func (f *File) GetChunk(index int) []Chunk {
	return f.Moov.GetTraks()[index].GetChunk()
}

func (f *File) GetSample(index int) []Sample {
	return f.Moov.GetTraks()[index].GetSample()
}

func (f *File) buildTrakTables() error {
	for _, trak := range f.Moov.Traks {
		trak.Chunks = make([]Chunk, trak.Mdia.Minf.Stbl.Stco.Entry_count)
		for i, offset := range trak.Mdia.Minf.Stbl.Stco.Chunk_offset {
			trak.Chunks[i].Offset = offset
		}

		sample_num := uint32(1)
		next_chunk_id := 1
		for i := 0; i < int(trak.Mdia.Minf.Stbl.Stsc.Entry_count); i++ {
			if i+1 < int(trak.Mdia.Minf.Stbl.Stsc.Entry_count) {
				next_chunk_id = int(trak.Mdia.Minf.Stbl.Stsc.First_chunk[i+1])
			} else {
				next_chunk_id = len(trak.Chunks)
			}
			first_chunk_id := trak.Mdia.Minf.Stbl.Stsc.First_chunk[i]
			n_samples := trak.Mdia.Minf.Stbl.Stsc.Samples_per_chunk[i]
			sdi := trak.Mdia.Minf.Stbl.Stsc.Sample_description_index[i]
			for j := int(first_chunk_id - 1); j < next_chunk_id; j++ {
				trak.Chunks[j].Sample_count = n_samples
				trak.Chunks[j].Sample_description_index = sdi
				trak.Chunks[j].Start_sample = sample_num
				sample_num += n_samples
			}
		}

		sample_count := int(trak.Mdia.Minf.Stbl.Stsz.Sample_count)
		trak.Samples = make([]Sample, sample_count)
		sample_size := trak.Mdia.Minf.Stbl.Stsz.Sample_size
		for i := 0; i < sample_count; i++ {
			if sample_size == uint32(0) {
				trak.Samples[i].Size = trak.Mdia.Minf.Stbl.Stsz.Entry_size[i]
			} else {
				trak.Samples[i].Size = sample_size
			}
		}

		// Calculate file offset for each sample
		sample_id := 0
		for i := 0; i < len(trak.Chunks); i++ {
			sample_offset := trak.Chunks[i].Offset
			for j := 0; j < int(trak.Chunks[i].Sample_count); j++ {
				sample_offset += trak.Samples[sample_id].Size
				sample_id++
			}
		}

		// Calculate decoding time for each sample
		sample_id, sample_time := 0, uint32(0)
		for i := 0; i < int(trak.Mdia.Minf.Stbl.Stts.Entry_count); i++ {
			sample_duration := trak.Mdia.Minf.Stbl.Stts.Sample_delta[i]
			for j := 0; j < int(trak.Mdia.Minf.Stbl.Stts.Sample_count[i]); j++ {
				trak.Samples[sample_id].Start_time = sample_time
				trak.Samples[sample_id].Duration = sample_duration
				sample_time += sample_duration
				sample_id++
			}
		}
		// Calculate decoding to composition time offset, if ctts table exists
		if trak.Mdia.Minf.Stbl.Ctts != nil {
			sample_id = 0
			for i := 0; i < int(trak.Mdia.Minf.Stbl.Ctts.Entry_count); i++ {
				count := int(trak.Mdia.Minf.Stbl.Ctts.Sample_count[i])
				cto := trak.Mdia.Minf.Stbl.Ctts.Sample_offset[i]
				for j := 0; j < count; j++ {
					trak.Samples[sample_id].Cto = cto
					sample_id++
				}
			}
		}
	}
	return nil
}

func readBoxes(f *File, start int64, n int64) (boxes chan *Box) {
	boxes = make(chan *Box, 100)
	go func() {
		for offset := start; offset < start+n; {
			size, name := f.ReadBoxAt(offset)
			fmt.Printf("Box found:\nType: %v \nSize (bytes): %v \n", name, size)

			box := &Box{
				Name:  name,
				Size:  int64(size),
				Start: offset,
				File:  f,
			}
			boxes <- box
			offset += int64(size)
		}
		close(boxes)
	}()
	return boxes
}

func readSubBoxes(f *File, start int64, n int64) (boxes chan *Box) {
	return readBoxes(f, start+BOX_HEADER_SIZE, n-BOX_HEADER_SIZE)
}

type File struct {
	*os.File
	Ftyp *FtypBox
	Moov *MoovBox
	Mdat *Box
	Size int64
}

func (f *File) ReadBoxAt(offset int64) (boxSize uint32, boxType string) {
	// Get Box size
	buf := f.ReadBytesAt(BOX_HEADER_SIZE, offset)
	boxSize = binary.BigEndian.Uint32(buf[0:4])
	offset += BOX_HEADER_SIZE
	// Get Box name
	boxType = string(buf[4:8])
	return boxSize, boxType
}

func (f *File) ReadBytesAt(n int64, offset int64) (word []byte) {
	buf := make([]byte, n)
	if _, error := f.ReadAt(buf, offset); error != nil {
		fmt.Println(error)
		return
	}
	return buf
}

type BoxInt interface {
	Name() string
	File() *File
	Size() int64
	Start() int64
	parse() error
}

type Box struct {
	Name        string
	Size, Start int64
	File        *File
}

// func (b *Box) Name() string { return b.Name }

// func (b *Box) Size() int64 { return b.Size }

// func (b *Box) File() *File { return b.File }

// func (b *Box) Start() int64 { return b.Start }

func (b *Box) parse() error {
	fmt.Printf("Default parser called; skip parsing. (%v)\n", b.Name)
	return nil
}

func (b *Box) ReadBoxData() []byte {
	if b.Size <= BOX_HEADER_SIZE {
		return nil
	}
	return b.File.ReadBytesAt(b.Size-BOX_HEADER_SIZE, b.Start+BOX_HEADER_SIZE)
}

type FtypBox struct {
	*Box
	Major_brand, Minor_version string
	Compatible_brands          []string
}

func (b *FtypBox) parse() error {
	data := b.ReadBoxData()
	b.Major_brand, b.Minor_version = string(data[0:4]), string(data[4:8])
	if len(data) > 8 {
		for i := 8; i < len(data); i += 4 {
			b.Compatible_brands = append(b.Compatible_brands, string(data[i:i+4]))
		}
	}
	return nil
}

type MoovBox struct {
	*Box
	Mvhd  *MvhdBox
	Iods  *IodsBox
	Traks []*TrakBox
	Udta  *UdtaBox
}

func (b *MoovBox) GetTraks() []*TrakBox {
	return b.Traks
}

func (b *MoovBox) parse() error {
	boxes := readSubBoxes(b.File, b.Start, b.Size)
	for subBox := range boxes {
		switch subBox.Name {
		case "mvhd":
			b.Mvhd = &MvhdBox{Box: subBox}
			b.Mvhd.parse()
		case "iods":
			b.Iods = &IodsBox{Box: subBox}
			b.Iods.parse()
		case "trak":
			trak := &TrakBox{Box: subBox}
			trak.parse()
			b.Traks = append(b.Traks, trak)
		case "udta":
			b.Udta = &UdtaBox{Box: subBox}
			b.Udta.parse()
		default:
			fmt.Printf("Unhandled Moov Sub-Box: %v \n", subBox.Name)
		}
	}
	return nil
}

type MvhdBox struct {
	*Box
	Version                                                              uint8
	Flags                                                                [3]byte
	Creation_time, Modification_time, Timescale, Duration, Next_track_id uint32
	Rate                                                                 Fixed32
	Volume                                                               Fixed16
	Other_data                                                           []byte
}

func (b *MvhdBox) parse() (err error) {
	data := b.ReadBoxData()
	b.Version = data[0]
	b.Flags = [3]byte{data[1], data[2], data[3]}
	b.Creation_time = binary.BigEndian.Uint32(data[4:8])
	b.Modification_time = binary.BigEndian.Uint32(data[8:12])
	b.Timescale = binary.BigEndian.Uint32(data[12:16])
	b.Duration = binary.BigEndian.Uint32(data[16:20])
	b.Rate, err = MakeFixed32(data[20:24])
	if err != nil {
		return err
	}
	b.Volume, err = MakeFixed16(data[24:26])
	if err != nil {
		return err
	}
	b.Other_data = data[26:]
	return nil
}

type IodsBox struct {
	*Box
	Data []byte
}

func (b *IodsBox) parse() error {
	b.Data = b.ReadBoxData()
	return nil
}

type TrakBox struct {
	*Box
	Tkhd    *TkhdBox
	Mdia    *MdiaBox
	Edts    *EdtsBox
	Chunks  []Chunk
	Samples []Sample
}

func (b *TrakBox) PrintChunk() {
	for k, v := range b.Chunks {
		fmt.Printf("Chunk %d, offset %d, sample_count %d, desc %d, start_sample %d\n", k, v.Offset, v.Sample_count, v.Sample_description_index, v.Start_sample)
	}
}

func (b *TrakBox) GetChunk() []Chunk {
	return b.Chunks
}

func (b *TrakBox) GetSample() []Sample {
	return b.Samples
}

func (b *TrakBox) PrintSample() {
	for k, v := range b.Samples {
		fmt.Printf("Sample %d, offset %d, size %d, duration %d, start_time %d, cto: %d\n", k, v.Offset, v.Size, v.Duration, v.Start_time, v.Cto)
	}
}

func (b *TrakBox) parse() error {
	boxes := readSubBoxes(b.File, b.Start, b.Size)
	for subBox := range boxes {
		switch subBox.Name {
		case "tkhd":
			b.Tkhd = &TkhdBox{Box: subBox}
			b.Tkhd.parse()
		case "mdia":
			b.Mdia = &MdiaBox{Box: subBox}
			b.Mdia.parse()
		case "edts":
			b.Edts = &EdtsBox{Box: subBox}
			b.Edts.parse()
		default:
			fmt.Printf("Unhandled Trak Sub-Box: %v \n", subBox.Name)
		}
	}
	return nil
}

type TkhdBox struct {
	*Box
	Version                                              uint8
	Flags                                                [3]byte
	Creation_time, Modification_time, Track_id, Duration uint32
	Layer, Alternate_group                               uint16 // This should really be int16 but not sure how to parse
	Volume                                               Fixed16
	Matrix                                               []byte
	Width, Height                                        Fixed32
}

func (b *TkhdBox) parse() (err error) {
	data := b.ReadBoxData()
	b.Version = data[0]
	b.Flags = [3]byte{data[1], data[2], data[3]}
	b.Creation_time = binary.BigEndian.Uint32(data[4:8])
	b.Modification_time = binary.BigEndian.Uint32(data[8:12])
	b.Track_id = binary.BigEndian.Uint32(data[12:16])
	// Skip 4 bytes for reserved space (uint32)
	b.Duration = binary.BigEndian.Uint32(data[20:24])
	// Skip 8 bytes for reserved space (2 uint32)
	b.Layer = binary.BigEndian.Uint16(data[32:34])
	b.Alternate_group = binary.BigEndian.Uint16(data[34:36])
	b.Volume, err = MakeFixed16(data[36:38])
	if err != nil {
		return err
	}
	// Skip 2 bytes for reserved space (uint16)
	b.Matrix = data[40:76]
	b.Width, err = MakeFixed32(data[76:80])
	if err != nil {
		return err
	}
	b.Height, err = MakeFixed32(data[80:84])
	if err != nil {
		return err
	}
	return nil
}

type EdtsBox struct {
	*Box
	Elst *ElstBox
}

func (b *EdtsBox) parse() (err error) {
	boxes := readSubBoxes(b.File, b.Start, b.Size)
	for subBox := range boxes {
		switch subBox.Name {
		case "elst":
			b.Elst = &ElstBox{Box: subBox}
			err = b.Elst.parse()
		default:
			fmt.Printf("Unhandled Edts Sub-Box: %v \n", subBox.Name)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

type ElstBox struct {
	*Box
	Version                                 uint8
	Flags                                   [3]byte
	Entry_count                             uint32
	Segment_duration, Media_time            []uint32
	Media_rate_integer, Media_rate_fraction []uint16 // This should really be int16 but not sure how to parse
}

func (b *ElstBox) parse() (err error) {
	data := b.ReadBoxData()
	b.Version = data[0]
	b.Flags = [3]byte{data[1], data[2], data[3]}
	b.Entry_count = binary.BigEndian.Uint32(data[4:8])
	for i := 0; i < int(b.Entry_count); i++ {
		sd := binary.BigEndian.Uint32(data[(8 + 12*i):(12 + 12*i)])
		mt := binary.BigEndian.Uint32(data[(12 + 12*i):(16 + 12*i)])
		mri := binary.BigEndian.Uint16(data[(16 + 12*i):(18 + 12*i)])
		mrf := binary.BigEndian.Uint16(data[(18 + 12*i):(20 + 12*i)])
		b.Segment_duration = append(b.Segment_duration, sd)
		b.Media_time = append(b.Media_time, mt)
		b.Media_rate_integer = append(b.Media_rate_integer, mri)
		b.Media_rate_fraction = append(b.Media_rate_fraction, mrf)
	}
	return nil
}

type MdiaBox struct {
	*Box
	Mdhd *MdhdBox
	Hdlr *HdlrBox
	Minf *MinfBox
}

func (b *MdiaBox) parse() error {
	boxes := readSubBoxes(b.File, b.Start, b.Size)
	for subBox := range boxes {
		switch subBox.Name {
		case "mdhd":
			b.Mdhd = &MdhdBox{Box: subBox}
			b.Mdhd.parse()
		case "hdlr":
			b.Hdlr = &HdlrBox{Box: subBox}
			b.Hdlr.parse()
		case "minf":
			b.Minf = &MinfBox{Box: subBox}
			b.Minf.parse()
		default:
			fmt.Printf("Unhandled Mdia Sub-Box: %v \n", subBox.Name)
		}
	}
	return nil
}

type MdhdBox struct {
	*Box
	Version                                               uint8
	Flags                                                 [3]byte
	Creation_time, Modification_time, Timescale, Duration uint32
	Language                                              uint16 // Combine 1-bit padding w/ 15-bit language data
}

func (b *MdhdBox) parse() (err error) {
	data := b.ReadBoxData()
	b.Version = data[0]
	b.Flags = [3]byte{data[1], data[2], data[3]}
	b.Creation_time = binary.BigEndian.Uint32(data[4:8])
	b.Modification_time = binary.BigEndian.Uint32(data[8:12])
	b.Timescale = binary.BigEndian.Uint32(data[12:16])
	b.Duration = binary.BigEndian.Uint32(data[16:20])
	// language includes 1 padding bit
	b.Language = binary.BigEndian.Uint16(data[20:22])
	return nil
}

type HdlrBox struct {
	*Box
	Version                  uint8
	Flags                    [3]byte
	Pre_defined              uint32
	Handler_type, Track_name string
}

func (b *HdlrBox) parse() (err error) {
	data := b.ReadBoxData()
	b.Version = data[0]
	b.Flags = [3]byte{data[1], data[2], data[3]}
	b.Pre_defined = binary.BigEndian.Uint32(data[4:8])
	b.Handler_type = string(data[8:12])
	// Skip 12 bytes for reserved space (3 uint32)
	b.Track_name = string(data[24:])
	return nil
}

type MinfBox struct {
	*Box
	Vmhd *VmhdBox
	Smhd *SmhdBox
	Stbl *StblBox
	Dinf *DinfBox
	Hdlr *HdlrBox
}

func (b *MinfBox) parse() (err error) {
	boxes := readSubBoxes(b.File, b.Start, b.Size)
	for subBox := range boxes {
		switch subBox.Name {
		case "vmhd":
			b.Vmhd = &VmhdBox{Box: subBox}
			err = b.Vmhd.parse()
		case "smhd":
			b.Smhd = &SmhdBox{Box: subBox}
			err = b.Smhd.parse()
		case "stbl":
			b.Stbl = &StblBox{Box: subBox}
			err = b.Stbl.parse()
		case "dinf":
			b.Dinf = &DinfBox{Box: subBox}
			err = b.Dinf.parse()
		case "hdlr":
			b.Hdlr = &HdlrBox{Box: subBox}
			err = b.Hdlr.parse()
		default:
			fmt.Printf("Unhandled Minf Sub-Box: %v \n", subBox.Name)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

type VmhdBox struct {
	*Box
	Version      uint8
	Flags        [3]byte
	Graphicsmode uint16
	Opcolor      [3]uint16
}

func (b *VmhdBox) parse() (err error) {
	data := b.ReadBoxData()
	b.Version = data[0]
	b.Flags = [3]byte{data[1], data[2], data[3]}
	b.Graphicsmode = binary.BigEndian.Uint16(data[4:6])
	for i := 0; i < 3; i++ {
		b.Opcolor[i] = binary.BigEndian.Uint16(data[(6 + 2*i):(8 + 2*i)])
	}
	return nil
}

type SmhdBox struct {
	*Box
	Version uint8
	Flags   [3]byte
	Balance uint16 // This should really be int16 but not sure how to parse
}

func (b *SmhdBox) parse() (err error) {
	data := b.ReadBoxData()
	b.Version = data[0]
	b.Flags = [3]byte{data[1], data[2], data[3]}
	b.Balance = binary.BigEndian.Uint16(data[4:6])
	return nil
}

type StblBox struct {
	*Box
	Stsd *StsdBox
	Stts *SttsBox
	Stss *StssBox
	Stsc *StscBox
	Stsz *StszBox
	Stco *StcoBox
	Ctts *CttsBox
}

func (b *StblBox) parse() (err error) {
	boxes := readSubBoxes(b.File, b.Start, b.Size)
	for subBox := range boxes {
		switch subBox.Name {
		case "stsd":
			b.Stsd = &StsdBox{Box: subBox}
			err = b.Stsd.parse()
		case "stts":
			b.Stts = &SttsBox{Box: subBox}
			err = b.Stts.parse()
		case "stss":
			b.Stss = &StssBox{Box: subBox}
			err = b.Stss.parse()
		case "stsc":
			b.Stsc = &StscBox{Box: subBox}
			err = b.Stsc.parse()
		case "stsz":
			b.Stsz = &StszBox{Box: subBox}
			err = b.Stsz.parse()
		case "stco":
			b.Stco = &StcoBox{Box: subBox}
			err = b.Stco.parse()
		case "ctts":
			b.Ctts = &CttsBox{Box: subBox}
			err = b.Ctts.parse()
		default:
			fmt.Printf("Unhandled Stbl Sub-Box: %v \n", subBox.Name)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

type StsdBox struct {
	*Box
	Version     uint8
	Flags       [3]byte
	Entry_count uint32
	Other_data  []byte
}

func (b *StsdBox) parse() (err error) {
	data := b.ReadBoxData()
	b.Version = data[0]
	b.Flags = [3]byte{data[1], data[2], data[3]}
	b.Entry_count = binary.BigEndian.Uint32(data[4:8])
	b.Other_data = data[8:]
	fmt.Println("stsd box parsing not yet finished")
	return nil
}

type SttsBox struct {
	*Box
	Version      uint8
	Flags        [3]byte
	Entry_count  uint32
	Sample_count []uint32
	Sample_delta []uint32
}

func (b *SttsBox) parse() (err error) {
	data := b.ReadBoxData()
	b.Version = data[0]
	b.Flags = [3]byte{data[1], data[2], data[3]}
	b.Entry_count = binary.BigEndian.Uint32(data[4:8])
	for i := 0; i < int(b.Entry_count); i++ {
		s_count := binary.BigEndian.Uint32(data[(8 + 8*i):(12 + 8*i)])
		s_delta := binary.BigEndian.Uint32(data[(12 + 8*i):(16 + 8*i)])
		b.Sample_count = append(b.Sample_count, s_count)
		b.Sample_delta = append(b.Sample_delta, s_delta)
	}
	return nil
}

type StssBox struct {
	*Box
	Version       uint8
	Flags         [3]byte
	Entry_count   uint32
	Sample_number []uint32
}

func (b *StssBox) parse() (err error) {
	data := b.ReadBoxData()
	b.Version = data[0]
	b.Flags = [3]byte{data[1], data[2], data[3]}
	b.Entry_count = binary.BigEndian.Uint32(data[4:8])
	for i := 0; i < int(b.Entry_count); i++ {
		sample := binary.BigEndian.Uint32(data[(8 + 4*i):(12 + 4*i)])
		b.Sample_number = append(b.Sample_number, sample)
	}
	return nil
}

type StscBox struct {
	*Box
	Version                  uint8
	Flags                    [3]byte
	Entry_count              uint32
	First_chunk              []uint32
	Samples_per_chunk        []uint32
	Sample_description_index []uint32
}

func (b *StscBox) parse() (err error) {
	data := b.ReadBoxData()
	b.Version = data[0]
	b.Flags = [3]byte{data[1], data[2], data[3]}
	b.Entry_count = binary.BigEndian.Uint32(data[4:8])
	for i := 0; i < int(b.Entry_count); i++ {
		fc := binary.BigEndian.Uint32(data[(8 + 12*i):(12 + 12*i)])
		spc := binary.BigEndian.Uint32(data[(12 + 12*i):(16 + 12*i)])
		sdi := binary.BigEndian.Uint32(data[(16 + 12*i):(20 + 12*i)])
		b.First_chunk = append(b.First_chunk, fc)
		b.Samples_per_chunk = append(b.Samples_per_chunk, spc)
		b.Sample_description_index = append(b.Sample_description_index, sdi)
	}
	return nil
}

type StszBox struct {
	*Box
	Version      uint8
	Flags        [3]byte
	Sample_size  uint32
	Sample_count uint32
	Entry_size   []uint32
}

func (b *StszBox) parse() (err error) {
	data := b.ReadBoxData()
	b.Version = data[0]
	b.Flags = [3]byte{data[1], data[2], data[3]}
	b.Sample_size = binary.BigEndian.Uint32(data[4:8])
	b.Sample_count = binary.BigEndian.Uint32(data[8:12])
	if b.Sample_size == uint32(0) {
		for i := 0; i < int(b.Sample_count); i++ {
			entry := binary.BigEndian.Uint32(data[(12 + 4*i):(16 + 4*i)])
			b.Entry_size = append(b.Entry_size, entry)
		}
	}
	return nil
}

type StcoBox struct {
	*Box
	Version      uint8
	Flags        [3]byte
	Entry_count  uint32
	Chunk_offset []uint32
}

func (b *StcoBox) parse() (err error) {
	data := b.ReadBoxData()
	b.Version = data[0]
	b.Flags = [3]byte{data[1], data[2], data[3]}
	b.Entry_count = binary.BigEndian.Uint32(data[4:8])
	for i := 0; i < int(b.Entry_count); i++ {
		chunk := binary.BigEndian.Uint32(data[(8 + 4*i):(12 + 4*i)])
		b.Chunk_offset = append(b.Chunk_offset, chunk)
	}
	return nil
}

type CttsBox struct {
	*Box
	Version       uint8
	Flags         [3]byte
	Entry_count   uint32
	Sample_count  []uint32
	Sample_offset []uint32
}

func (b *CttsBox) parse() (err error) {
	data := b.ReadBoxData()
	b.Version = data[0]
	b.Flags = [3]byte{data[1], data[2], data[3]}
	b.Entry_count = binary.BigEndian.Uint32(data[4:8])
	for i := 0; i < int(b.Entry_count); i++ {
		s_count := binary.BigEndian.Uint32(data[(8 + 8*i):(12 + 8*i)])
		s_offset := binary.BigEndian.Uint32(data[(12 + 8*i):(16 + 8*i)])
		b.Sample_count = append(b.Sample_count, s_count)
		b.Sample_offset = append(b.Sample_offset, s_offset)
	}
	return nil
}

type DinfBox struct {
	*Box
	Dref *DrefBox
}

func (b *DinfBox) parse() (err error) {
	boxes := readSubBoxes(b.File, b.Start, b.Size)
	for subBox := range boxes {
		switch subBox.Name {
		case "dref":
			b.Dref = &DrefBox{Box: subBox}
			err = b.Dref.parse()
		default:
			fmt.Printf("Unhandled Dinf Sub-Box: %v \n", subBox.Name)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

type DrefBox struct {
	*Box
	Version     uint8
	Flags       [3]byte
	Entry_count uint32
	Other_data  []byte
}

func (b *DrefBox) parse() (err error) {
	data := b.ReadBoxData()
	b.Version = data[0]
	b.Flags = [3]byte{data[1], data[2], data[3]}
	b.Entry_count = binary.BigEndian.Uint32(data[4:8])
	b.Other_data = data[8:]
	fmt.Println("dref box parsing not yet finished")
	return nil
}

type UdtaBox struct {
	*Box
	Meta *MetaBox
}

func (b *UdtaBox) parse() (err error) {
	boxes := readSubBoxes(b.File, b.Start, b.Size)
	for subBox := range boxes {
		switch subBox.Name {
		case "meta":
			b.Meta = &MetaBox{Box: subBox}
			err = b.Meta.parse()
		default:
			fmt.Printf("Unhandled Udta Sub-Box: %v \n", subBox.Name)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

type MetaBox struct {
	*Box
	Version uint8
	Flags   [3]byte
	Hdlr    *HdlrBox
}

func (b *MetaBox) parse() (err error) {
	data := b.ReadBoxData()
	b.Version = data[0]
	b.Flags = [3]byte{data[1], data[2], data[3]}
	boxes := readSubBoxes(b.File, b.Start+4, b.Size-4)
	for subBox := range boxes {
		switch subBox.Name {
		case "hdlr":
			b.Hdlr = &HdlrBox{Box: subBox}
			err = b.Hdlr.parse()
		default:
			fmt.Printf("Unhandled Meta Sub-Box: %v \n", subBox.Name)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// An 8.8 Fixed Point Decimal notation
type Fixed16 uint16

func (f Fixed16) String() string {
	return fmt.Sprintf("%v", uint16(f)>>8)
}

func MakeFixed16(bytes []byte) (Fixed16, error) {
	if len(bytes) != 2 {
		return Fixed16(0), fmt.Errorf("Invalid number of bytes for Fixed16. Need 2, got " + string(len(bytes)))
	}
	return Fixed16(binary.BigEndian.Uint16(bytes)), nil
}

// A 16.16 Fixed Point Decimal notation
type Fixed32 uint32

func (f Fixed32) String() string {
	return fmt.Sprintf("%v", uint32(f)>>16)
}

func MakeFixed32(bytes []byte) (Fixed32, error) {
	if len(bytes) != 4 {
		return Fixed32(0), fmt.Errorf("Invalid number of bytes for Fixed32. Need 4, got " + string(len(bytes)))
	}
	return Fixed32(binary.BigEndian.Uint32(bytes)), nil
}

type Chunk struct {
	Sample_description_index, Start_sample, Sample_count, Offset uint32
}

func (c *Chunk) GetOffset() uint32 {
	return c.Offset
}

func (c *Chunk) GetStartSample() uint32 {
	return c.Start_sample
}

type Sample struct {
	Size, Offset, Start_time, Duration, Cto uint32
}

func (s *Sample) GetSize() uint32 {
	return s.Size
}
