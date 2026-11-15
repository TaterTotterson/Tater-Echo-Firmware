package videostream

import "errors"

type h264BitReader struct {
	value []byte
	bit   int
}

func (reader *h264BitReader) readBits(count int) (uint32, error) {
	if count < 0 || count > 32 || reader.bit+count > len(reader.value)*8 {
		return 0, errors.New("truncated H.264 SPS")
	}
	var result uint32
	for range count {
		result = result<<1 | uint32(reader.value[reader.bit/8]>>(7-reader.bit%8)&1)
		reader.bit++
	}
	return result, nil
}

func (reader *h264BitReader) readUE() (uint32, error) {
	leadingZeroes := 0
	for {
		bit, err := reader.readBits(1)
		if err != nil {
			return 0, err
		}
		if bit != 0 {
			break
		}
		leadingZeroes++
		if leadingZeroes > 31 {
			return 0, errors.New("invalid H.264 exponential Golomb value")
		}
	}
	if leadingZeroes == 0 {
		return 0, nil
	}
	suffix, err := reader.readBits(leadingZeroes)
	if err != nil {
		return 0, err
	}
	return ((uint32(1) << leadingZeroes) - 1) + suffix, nil
}

func (reader *h264BitReader) readSE() (int32, error) {
	value, err := reader.readUE()
	if err != nil {
		return 0, err
	}
	if value&1 != 0 {
		return int32((value + 1) / 2), nil
	}
	return -int32(value / 2), nil
}

func h264RBSP(value []byte) []byte {
	result := make([]byte, 0, len(value))
	zeroes := 0
	for _, current := range value {
		if zeroes >= 2 && current == 3 {
			zeroes = 0
			continue
		}
		result = append(result, current)
		if current == 0 {
			zeroes++
		} else {
			zeroes = 0
		}
	}
	return result
}

func h264StartCode(value []byte, from int) (offset, prefix int, found bool) {
	for offset = max(from, 0); offset+4 <= len(value); offset++ {
		switch {
		case offset+5 <= len(value) && value[offset] == 0 && value[offset+1] == 0 && value[offset+2] == 0 && value[offset+3] == 1:
			return offset, 4, true
		case value[offset] == 0 && value[offset+1] == 0 && value[offset+2] == 1:
			return offset, 3, true
		}
	}
	return 0, 0, false
}

func skipH264ScalingList(reader *h264BitReader, count int) error {
	lastScale, nextScale := int32(8), int32(8)
	for range count {
		if nextScale != 0 {
			delta, err := reader.readSE()
			if err != nil {
				return err
			}
			nextScale = (lastScale + delta + 256) % 256
		}
		if nextScale != 0 {
			lastScale = nextScale
		}
	}
	return nil
}

func parseH264SPS(value []byte) (width, height int, err error) {
	reader := &h264BitReader{value: h264RBSP(value)}
	profile, err := reader.readBits(8)
	if err != nil {
		return 0, 0, err
	}
	if _, err = reader.readBits(16); err != nil { // constraint flags and level
		return 0, 0, err
	}
	if _, err = reader.readUE(); err != nil { // SPS id
		return 0, 0, err
	}
	chromaFormat := uint32(1)
	separateColourPlane := uint32(0)
	switch profile {
	case 100, 110, 122, 244, 44, 83, 86, 118, 128, 138, 139, 134, 135:
		if chromaFormat, err = reader.readUE(); err != nil || chromaFormat > 3 {
			return 0, 0, errors.New("invalid H.264 chroma format")
		}
		if chromaFormat == 3 {
			if separateColourPlane, err = reader.readBits(1); err != nil {
				return 0, 0, err
			}
		}
		if _, err = reader.readUE(); err != nil { // bit depth luma
			return 0, 0, err
		}
		if _, err = reader.readUE(); err != nil { // bit depth chroma
			return 0, 0, err
		}
		if _, err = reader.readBits(1); err != nil { // transform bypass
			return 0, 0, err
		}
		scalingMatrix, matrixErr := reader.readBits(1)
		if matrixErr != nil {
			return 0, 0, matrixErr
		}
		if scalingMatrix != 0 {
			lists := 8
			if chromaFormat == 3 {
				lists = 12
			}
			for index := range lists {
				present, readErr := reader.readBits(1)
				if readErr != nil {
					return 0, 0, readErr
				}
				if present != 0 {
					size := 16
					if index >= 6 {
						size = 64
					}
					if err := skipH264ScalingList(reader, size); err != nil {
						return 0, 0, err
					}
				}
			}
		}
	}
	if _, err = reader.readUE(); err != nil { // log2 max frame num
		return 0, 0, err
	}
	pictureOrderCount, err := reader.readUE()
	if err != nil {
		return 0, 0, err
	}
	switch pictureOrderCount {
	case 0:
		_, err = reader.readUE()
	case 1:
		if _, err = reader.readBits(1); err == nil {
			_, err = reader.readSE()
		}
		if err == nil {
			_, err = reader.readSE()
		}
		var cycle uint32
		if err == nil {
			cycle, err = reader.readUE()
		}
		if cycle > 255 {
			return 0, 0, errors.New("invalid H.264 picture-order cycle")
		}
		for index := uint32(0); err == nil && index < cycle; index++ {
			_, err = reader.readSE()
		}
	case 2:
		// This valid POC mode has no additional syntax elements.
	default:
		return 0, 0, errors.New("invalid H.264 picture-order mode")
	}
	if err != nil {
		return 0, 0, err
	}
	if _, err = reader.readUE(); err != nil { // max reference frames
		return 0, 0, err
	}
	if _, err = reader.readBits(1); err != nil { // gaps allowed
		return 0, 0, err
	}
	widthMbs, err := reader.readUE()
	if err != nil {
		return 0, 0, err
	}
	heightMapUnits, err := reader.readUE()
	if err != nil {
		return 0, 0, err
	}
	frameMbsOnly, err := reader.readBits(1)
	if err != nil {
		return 0, 0, err
	}
	if frameMbsOnly == 0 {
		if _, err = reader.readBits(1); err != nil {
			return 0, 0, err
		}
	}
	if _, err = reader.readBits(1); err != nil { // direct 8x8 inference
		return 0, 0, err
	}
	cropLeft, cropRight, cropTop, cropBottom := uint32(0), uint32(0), uint32(0), uint32(0)
	cropping, err := reader.readBits(1)
	if err != nil {
		return 0, 0, err
	}
	if cropping != 0 {
		if cropLeft, err = reader.readUE(); err == nil {
			cropRight, err = reader.readUE()
		}
		if err == nil {
			cropTop, err = reader.readUE()
		}
		if err == nil {
			cropBottom, err = reader.readUE()
		}
		if err != nil {
			return 0, 0, err
		}
	}
	codedWidth := (uint64(widthMbs) + 1) * 16
	codedHeight := (uint64(heightMapUnits) + 1) * 16 * uint64(2-frameMbsOnly)
	cropUnitX, cropUnitY := uint64(1), uint64(2-frameMbsOnly)
	chromaArrayType := chromaFormat
	if separateColourPlane != 0 {
		chromaArrayType = 0
	}
	if chromaArrayType != 0 {
		subWidth := []uint64{0, 2, 2, 1}[chromaArrayType]
		subHeight := []uint64{0, 2, 1, 1}[chromaArrayType]
		cropUnitX = subWidth
		cropUnitY = subHeight * uint64(2-frameMbsOnly)
	}
	croppedX := (uint64(cropLeft) + uint64(cropRight)) * cropUnitX
	croppedY := (uint64(cropTop) + uint64(cropBottom)) * cropUnitY
	if codedWidth <= croppedX || codedHeight <= croppedY {
		return 0, 0, errors.New("invalid H.264 frame crop")
	}
	width, height = int(codedWidth-croppedX), int(codedHeight-croppedY)
	if width < 16 || width > vdecMaximumWidth || height < 16 || height > vdecMaximumHeight || width&1 != 0 || height&1 != 0 {
		return 0, 0, errors.New("H.264 frame geometry exceeds the hardware boundary")
	}
	return width, height, nil
}

func h264DimensionsFromAccessUnit(accessUnit []byte) (width, height int, err error) {
	for search := 0; ; {
		start, prefix, found := h264StartCode(accessUnit, search)
		if !found {
			break
		}
		next, _, nextFound := h264StartCode(accessUnit, start+prefix+1)
		if !nextFound {
			next = len(accessUnit)
		}
		if accessUnit[start+prefix]&0x1f == 7 {
			return parseH264SPS(accessUnit[start+prefix+1 : next])
		}
		search = next
	}
	return 0, 0, errors.New("first H.264 access unit has no SPS")
}
