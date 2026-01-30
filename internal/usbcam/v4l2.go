package usbcam

import (
	"encoding/binary"
	"fmt"
	"sort"
	"syscall"
	"unsafe"
)

// V4L2 ioctl request codes (Linux x86_64)
const (
	vidiocQuerycap          = 0x80685600 // VIDIOC_QUERYCAP
	vidiocEnumFmt           = 0xC0405602 // VIDIOC_ENUM_FMT
	vidiocEnumFramesizes    = 0xC02C564A // VIDIOC_ENUM_FRAMESIZES
	vidiocEnumFrameintervals = 0xC034564B // VIDIOC_ENUM_FRAMEINTERVALS
)

// V4L2 capability flags
const (
	v4l2CapVideoCapture       = 0x00000001
	v4l2CapVideoCaptureMplane = 0x00001000
	v4l2CapDeviceCaps         = 0x80000000
)

// V4L2 buffer and frame type constants
const (
	v4l2BufTypeVideoCapture = 1
	v4l2FrmsizeTypeDiscrete = 1
	v4l2FrmivalTypeDiscrete = 1
)

// v4l2Capability matches struct v4l2_capability (104 bytes)
type v4l2Capability struct {
	Driver       [16]byte
	Card         [32]byte
	BusInfo      [32]byte
	Version      uint32
	Capabilities uint32
	DeviceCaps   uint32
	Reserved     [3]uint32
}

// v4l2FmtDesc matches struct v4l2_fmtdesc (64 bytes)
type v4l2FmtDesc struct {
	Index       uint32
	Type        uint32
	Flags       uint32
	Description [32]byte
	PixelFormat uint32
	MbusCode    uint32
	Reserved    [3]uint32
}

// v4l2FrmSizeEnum matches struct v4l2_frmsizeenum (44 bytes)
type v4l2FrmSizeEnum struct {
	Index       uint32
	PixelFormat uint32
	Type        uint32
	Union       [24]byte // discrete (w,h) or stepwise (min/max/step)
	Reserved    [2]uint32
}

// v4l2FrmIvalEnum matches struct v4l2_frmivalenum (52 bytes)
type v4l2FrmIvalEnum struct {
	Index       uint32
	PixelFormat uint32
	Width       uint32
	Height      uint32
	Type        uint32
	Union       [24]byte // discrete (fract) or stepwise (min/max/step fracts)
	Reserved    [2]uint32
}

// queryCapability runs VIDIOC_QUERYCAP on the given device path.
func queryCapability(devPath string) (*v4l2Capability, error) {
	fd, err := syscall.Open(devPath, syscall.O_RDONLY, 0)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", devPath, err)
	}
	defer syscall.Close(fd)

	var cap v4l2Capability
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), vidiocQuerycap, uintptr(unsafe.Pointer(&cap)))
	if errno != 0 {
		return nil, fmt.Errorf("VIDIOC_QUERYCAP on %s: %w", devPath, errno)
	}
	return &cap, nil
}

// isVideoCaptureDevice checks if the capability indicates a video capture device.
// Uses device_caps when available (per-node capabilities on multi-function devices).
func isVideoCaptureDevice(cap *v4l2Capability) bool {
	caps := cap.Capabilities
	if caps&v4l2CapDeviceCaps != 0 {
		caps = cap.DeviceCaps
	}
	return caps&v4l2CapVideoCapture != 0 || caps&v4l2CapVideoCaptureMplane != 0
}

// enumFormats enumerates all supported video formats, frame sizes, and frame rates
// for the given device using V4L2 ioctls.
func enumFormats(devPath string) ([]VideoFormat, error) {
	fd, err := syscall.Open(devPath, syscall.O_RDONLY, 0)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", devPath, err)
	}
	defer syscall.Close(fd)

	var formats []VideoFormat

	// Enumerate pixel formats
	for fmtIdx := uint32(0); ; fmtIdx++ {
		var desc v4l2FmtDesc
		desc.Index = fmtIdx
		desc.Type = v4l2BufTypeVideoCapture

		_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), vidiocEnumFmt, uintptr(unsafe.Pointer(&desc)))
		if errno != 0 {
			break // EINVAL means no more formats
		}

		pixFmt := fourccToString(desc.PixelFormat)

		// Enumerate frame sizes for this pixel format
		for sizeIdx := uint32(0); ; sizeIdx++ {
			var frmSize v4l2FrmSizeEnum
			frmSize.Index = sizeIdx
			frmSize.PixelFormat = desc.PixelFormat

			_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), vidiocEnumFramesizes, uintptr(unsafe.Pointer(&frmSize)))
			if errno != 0 {
				break
			}

			if frmSize.Type != v4l2FrmsizeTypeDiscrete {
				continue // Skip stepwise/continuous sizes
			}

			width := binary.LittleEndian.Uint32(frmSize.Union[0:4])
			height := binary.LittleEndian.Uint32(frmSize.Union[4:8])

			// Enumerate frame intervals for this format+size
			var fpsList []int
			for ivalIdx := uint32(0); ; ivalIdx++ {
				var frmIval v4l2FrmIvalEnum
				frmIval.Index = ivalIdx
				frmIval.PixelFormat = desc.PixelFormat
				frmIval.Width = width
				frmIval.Height = height

				_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), vidiocEnumFrameintervals, uintptr(unsafe.Pointer(&frmIval)))
				if errno != 0 {
					break
				}

				if frmIval.Type != v4l2FrmivalTypeDiscrete {
					continue
				}

				// Discrete interval: numerator/denominator (v4l2_fract)
				numerator := binary.LittleEndian.Uint32(frmIval.Union[0:4])
				denominator := binary.LittleEndian.Uint32(frmIval.Union[4:8])
				if numerator > 0 {
					fps := int(denominator / numerator)
					if fps > 0 {
						fpsList = append(fpsList, fps)
					}
				}
			}

			// Deduplicate and sort FPS values (highest first)
			fpsList = deduplicateFPS(fpsList)

			formats = append(formats, VideoFormat{
				PixelFormat: pixFmt,
				Width:       int(width),
				Height:      int(height),
				FPS:         fpsList,
			})
		}
	}

	// Sort formats by resolution (highest first), then by pixel format
	sort.Slice(formats, func(i, j int) bool {
		resI := formats[i].Width * formats[i].Height
		resJ := formats[j].Width * formats[j].Height
		if resI != resJ {
			return resI > resJ
		}
		return formats[i].PixelFormat < formats[j].PixelFormat
	})

	return formats, nil
}

// fourccToString converts a V4L2 FourCC pixel format code to a string.
func fourccToString(fourcc uint32) string {
	return string([]byte{
		byte(fourcc & 0xFF),
		byte((fourcc >> 8) & 0xFF),
		byte((fourcc >> 16) & 0xFF),
		byte((fourcc >> 24) & 0xFF),
	})
}

// bytesToString extracts a null-terminated string from a byte array.
func bytesToString(b []byte) string {
	for i, c := range b {
		if c == 0 {
			return string(b[:i])
		}
	}
	return string(b)
}

// deduplicateFPS removes duplicate FPS values and sorts highest first.
func deduplicateFPS(fpsList []int) []int {
	if len(fpsList) == 0 {
		return fpsList
	}
	seen := make(map[int]bool)
	var unique []int
	for _, fps := range fpsList {
		if !seen[fps] {
			seen[fps] = true
			unique = append(unique, fps)
		}
	}
	sort.Sort(sort.Reverse(sort.IntSlice(unique)))
	return unique
}
