// +build linux,386 linux,amd64 linux,arm linux,arm64

package usbfs

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unsafe"

	"golang.org/x/sys/unix"
)

const (
	USBRQ_TYPE_VENDOR  = 0b01000000
	USBRQ_RCPT_DEVICE  = 0b00000000
	USBRQ_ENDPOINT_IN  = 0b10000000
	USBRQ_ENDPOINT_OUT = 0b00000000
)

type Device struct {
	bus  uint16
	dev  uint16
	path string
	open bool
	fd   int
}

type ctrlReq struct {
	ReqType uint8
	Req     uint8
	Value   uint16
	Index   uint16
	Len     uint16
	Timeout uint32
	Data    uintptr
}

func GetDevices(idVendor uint16, idProduct uint16, manufacturer string, product string) ([]*Device, error) {
	devices := []*Device{}

	err := filepath.Walk("/sys/bus/usb/devices", func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if info.Mode()&os.ModeSymlink == 0 {
			return nil
		}

		rpath, err := filepath.EvalSymlinks(path)
		if err != nil {
			return err
		}

		for _, d := range []struct {
			name     string
			toUint16 bool
			expected interface{}
		}{
			{"idVendor", true, idVendor},
			{"idProduct", true, idProduct},
			{"manufacturer", false, manufacturer},
			{"product", false, product},
		} {
			path := filepath.Join(rpath, d.name)
			_, err := os.Stat(path)
			if os.IsNotExist(err) {
				return nil
			}
			if err != nil {
				return err
			}
			b, err := ioutil.ReadFile(path)
			if err != nil {
				return err
			}
			valS := strings.TrimSpace(string(b))
			if d.toUint16 {
				valI, err := strconv.ParseUint(valS, 16, 16)
				if err != nil {
					return err
				}
				if uint16(valI) != d.expected.(uint16) {
					return nil
				}
			} else {
				if valS != d.expected.(string) {
					return nil
				}
			}
		}

		var busnum, devnum uint16
		for _, d := range []struct {
			name string
			val  *uint16
		}{
			{"busnum", &busnum},
			{"devnum", &devnum},
		} {
			path := filepath.Join(rpath, d.name)
			if _, err := os.Stat(path); err != nil {
				return err
			}
			b, err := ioutil.ReadFile(path)
			if err != nil {
				return err
			}
			valS := strings.TrimSpace(string(b))
			val, err := strconv.ParseUint(valS, 10, 16)
			if err != nil {
				return err
			}
			*d.val = uint16(val)
		}

		devices = append(devices, &Device{
			bus:  uint16(busnum),
			dev:  uint16(devnum),
			path: fmt.Sprintf("/dev/bus/usb/%03d/%03d", busnum, devnum),
		})

		return nil
	})

	return devices, err
}

func (d *Device) Open() error {
	if d.open {
		return fmt.Errorf("usb: device already open: %s", d.path)
	}

	fd, err := unix.Open(d.path, unix.O_RDWR, 0600)
	if err != nil {
		return err
	}
	d.fd = fd
	d.open = true
	return nil
}

func (d *Device) Close() error {
	if !d.open {
		return nil
	}
	d.open = false
	return unix.Close(d.fd)
}

func (d *Device) control(direction byte, request byte, val uint16, idx uint16, data []byte) error {
	if !d.open {
		return fmt.Errorf("usb: device is not open: %s", d.path)
	}
	var dataPointer uintptr
	if len(data) > 0 {
		dataPointer = uintptr(unsafe.Pointer(&data[0]))
	}
	// we need to fix dataPointer endianess in the (unlikely) case that we want to support big-endian platforms
	req := ctrlReq{
		USBRQ_TYPE_VENDOR | USBRQ_RCPT_DEVICE | direction,
		request,
		val,
		idx,
		uint16(len(data)),
		5000, // timeout
		dataPointer,
	}
	_, _, errno := unix.Syscall(
		unix.SYS_IOCTL,
		uintptr(d.fd),
		uintptr(USBDEVFS_CONTROL),
		uintptr(unsafe.Pointer(&req)),
	)
	if errno != 0 {
		return fmt.Errorf("usb: %s", errno)
	}
	return nil
}

func (d *Device) ControlIn(request byte, val uint16, idx uint16, data []byte) error {
	return d.control(USBRQ_ENDPOINT_IN, request, val, idx, data)
}

func (d *Device) ControlOut(request byte, val uint16, idx uint16, data []byte) error {
	return d.control(USBRQ_ENDPOINT_OUT, request, val, idx, data)
}
