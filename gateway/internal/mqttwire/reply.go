package mqttwire

import "io"

// Replies acknowledge connection, durable intake or liveness only. The service
// must complete authentication/admission before calling the applicable writer.
func WriteConnack(out io.Writer, reason byte) error {
	if reason > 5 {
		return ErrPacket
	}
	return write(out, []byte{0x20, 2, 0, reason})
}
func WritePuback(out io.Writer, id uint16) error {
	if id == 0 {
		return ErrPacket
	}
	return write(out, []byte{0x40, 2, byte(id >> 8), byte(id)})
}
func WritePingresp(out io.Writer) error { return write(out, []byte{0xd0, 0}) }

func write(out io.Writer, packet []byte) error {
	for len(packet) > 0 {
		n, err := out.Write(packet)
		if n < 0 || n > len(packet) {
			return io.ErrShortWrite
		}
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		packet = packet[n:]
	}
	return nil
}
