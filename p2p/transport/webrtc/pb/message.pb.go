// Code generated by protoc-gen-gogo. DO NOT EDIT.
// source: message.proto

package webrtc_pb

import (
	fmt "fmt"
	proto "github.com/gogo/protobuf/proto"
	io "io"
	math "math"
	math_bits "math/bits"
)

// Reference imports to suppress errors if they are not otherwise used.
var _ = proto.Marshal
var _ = fmt.Errorf
var _ = math.Inf

// This is a compile-time assertion to ensure that this generated file
// is compatible with the proto package it is being compiled against.
// A compilation error at this line likely means your copy of the
// proto package needs to be updated.
const _ = proto.GoGoProtoPackageIsVersion3 // please upgrade the proto package

type Message_Flag int32

const (
	// The local endpoint will no longer send messages.
	Message_CLOSE_WRITE Message_Flag = 0
	// The local endpoint will no longer read messages.
	Message_CLOSE_READ Message_Flag = 1
	// The local endpoint abruptly terminates the stream. The remote endpoint
	// may discard any in-flight data.
	Message_RESET Message_Flag = 2
)

var Message_Flag_name = map[int32]string{
	0: "CLOSE_WRITE",
	1: "CLOSE_READ",
	2: "RESET",
}

var Message_Flag_value = map[string]int32{
	"CLOSE_WRITE": 0,
	"CLOSE_READ":  1,
	"RESET":       2,
}

func (x Message_Flag) Enum() *Message_Flag {
	p := new(Message_Flag)
	*p = x
	return p
}

func (x Message_Flag) String() string {
	return proto.EnumName(Message_Flag_name, int32(x))
}

func (x *Message_Flag) UnmarshalJSON(data []byte) error {
	value, err := proto.UnmarshalJSONEnum(Message_Flag_value, data, "Message_Flag")
	if err != nil {
		return err
	}
	*x = Message_Flag(value)
	return nil
}

func (Message_Flag) EnumDescriptor() ([]byte, []int) {
	return fileDescriptor_33c57e4bae7b9afd, []int{0, 0}
}

type Message struct {
	Flag                 *Message_Flag `protobuf:"varint,1,opt,name=flag,enum=webrtc.pb.Message_Flag" json:"flag,omitempty"`
	Message              []byte        `protobuf:"bytes,2,opt,name=message" json:"message,omitempty"`
	XXX_NoUnkeyedLiteral struct{}      `json:"-"`
	XXX_unrecognized     []byte        `json:"-"`
	XXX_sizecache        int32         `json:"-"`
}

func (m *Message) Reset()         { *m = Message{} }
func (m *Message) String() string { return proto.CompactTextString(m) }
func (*Message) ProtoMessage()    {}
func (*Message) Descriptor() ([]byte, []int) {
	return fileDescriptor_33c57e4bae7b9afd, []int{0}
}
func (m *Message) XXX_Unmarshal(b []byte) error {
	return m.Unmarshal(b)
}
func (m *Message) XXX_Marshal(b []byte, deterministic bool) ([]byte, error) {
	if deterministic {
		return xxx_messageInfo_Message.Marshal(b, m, deterministic)
	} else {
		b = b[:cap(b)]
		n, err := m.MarshalToSizedBuffer(b)
		if err != nil {
			return nil, err
		}
		return b[:n], nil
	}
}
func (m *Message) XXX_Merge(src proto.Message) {
	xxx_messageInfo_Message.Merge(m, src)
}
func (m *Message) XXX_Size() int {
	return m.Size()
}
func (m *Message) XXX_DiscardUnknown() {
	xxx_messageInfo_Message.DiscardUnknown(m)
}

var xxx_messageInfo_Message proto.InternalMessageInfo

func (m *Message) GetFlag() Message_Flag {
	if m != nil && m.Flag != nil {
		return *m.Flag
	}
	return Message_CLOSE_WRITE
}

func (m *Message) GetMessage() []byte {
	if m != nil {
		return m.Message
	}
	return nil
}

func init() {
	proto.RegisterEnum("webrtc.pb.Message_Flag", Message_Flag_name, Message_Flag_value)
	proto.RegisterType((*Message)(nil), "webrtc.pb.Message")
}

func init() { proto.RegisterFile("message.proto", fileDescriptor_33c57e4bae7b9afd) }

var fileDescriptor_33c57e4bae7b9afd = []byte{
	// 163 bytes of a gzipped FileDescriptorProto
	0x1f, 0x8b, 0x08, 0x00, 0x00, 0x00, 0x00, 0x00, 0x02, 0xff, 0xe2, 0xe2, 0xcd, 0x4d, 0x2d, 0x2e,
	0x4e, 0x4c, 0x4f, 0xd5, 0x2b, 0x28, 0xca, 0x2f, 0xc9, 0x17, 0xe2, 0x2c, 0x4f, 0x4d, 0x2a, 0x2a,
	0x49, 0xd6, 0x2b, 0x48, 0x52, 0x6a, 0x61, 0xe4, 0x62, 0xf7, 0x85, 0x48, 0x0a, 0x69, 0x73, 0xb1,
	0xa4, 0xe5, 0x24, 0xa6, 0x4b, 0x30, 0x2a, 0x30, 0x6a, 0xf0, 0x19, 0x89, 0xeb, 0xc1, 0x55, 0xe9,
	0x41, 0x55, 0xe8, 0xb9, 0xe5, 0x24, 0xa6, 0x07, 0x81, 0x15, 0x09, 0x49, 0x70, 0xb1, 0x43, 0x0d,
	0x95, 0x60, 0x52, 0x60, 0xd4, 0xe0, 0x09, 0x82, 0x71, 0x95, 0x8c, 0xb8, 0x58, 0x40, 0xea, 0x84,
	0xf8, 0xb9, 0xb8, 0x9d, 0x7d, 0xfc, 0x83, 0x5d, 0xe3, 0xc3, 0x83, 0x3c, 0x43, 0x5c, 0x05, 0x18,
	0x84, 0xf8, 0xb8, 0xb8, 0x20, 0x02, 0x41, 0xae, 0x8e, 0x2e, 0x02, 0x8c, 0x42, 0x9c, 0x5c, 0xac,
	0x41, 0xae, 0xc1, 0xae, 0x21, 0x02, 0x4c, 0x4e, 0x3c, 0x27, 0x1e, 0xc9, 0x31, 0x5e, 0x78, 0x24,
	0xc7, 0xf8, 0xe0, 0x91, 0x1c, 0x23, 0x20, 0x00, 0x00, 0xff, 0xff, 0x58, 0x33, 0x0d, 0x31, 0xaf,
	0x00, 0x00, 0x00,
}

func (m *Message) Marshal() (dAtA []byte, err error) {
	size := m.Size()
	dAtA = make([]byte, size)
	n, err := m.MarshalToSizedBuffer(dAtA[:size])
	if err != nil {
		return nil, err
	}
	return dAtA[:n], nil
}

func (m *Message) MarshalTo(dAtA []byte) (int, error) {
	size := m.Size()
	return m.MarshalToSizedBuffer(dAtA[:size])
}

func (m *Message) MarshalToSizedBuffer(dAtA []byte) (int, error) {
	i := len(dAtA)
	_ = i
	var l int
	_ = l
	if m.XXX_unrecognized != nil {
		i -= len(m.XXX_unrecognized)
		copy(dAtA[i:], m.XXX_unrecognized)
	}
	if m.Message != nil {
		i -= len(m.Message)
		copy(dAtA[i:], m.Message)
		i = encodeVarintMessage(dAtA, i, uint64(len(m.Message)))
		i--
		dAtA[i] = 0x12
	}
	if m.Flag != nil {
		i = encodeVarintMessage(dAtA, i, uint64(*m.Flag))
		i--
		dAtA[i] = 0x8
	}
	return len(dAtA) - i, nil
}

func encodeVarintMessage(dAtA []byte, offset int, v uint64) int {
	offset -= sovMessage(v)
	base := offset
	for v >= 1<<7 {
		dAtA[offset] = uint8(v&0x7f | 0x80)
		v >>= 7
		offset++
	}
	dAtA[offset] = uint8(v)
	return base
}
func (m *Message) Size() (n int) {
	if m == nil {
		return 0
	}
	var l int
	_ = l
	if m.Flag != nil {
		n += 1 + sovMessage(uint64(*m.Flag))
	}
	if m.Message != nil {
		l = len(m.Message)
		n += 1 + l + sovMessage(uint64(l))
	}
	if m.XXX_unrecognized != nil {
		n += len(m.XXX_unrecognized)
	}
	return n
}

func sovMessage(x uint64) (n int) {
	return (math_bits.Len64(x|1) + 6) / 7
}
func sozMessage(x uint64) (n int) {
	return sovMessage(uint64((x << 1) ^ uint64((int64(x) >> 63))))
}
func (m *Message) Unmarshal(dAtA []byte) error {
	l := len(dAtA)
	iNdEx := 0
	for iNdEx < l {
		preIndex := iNdEx
		var wire uint64
		for shift := uint(0); ; shift += 7 {
			if shift >= 64 {
				return ErrIntOverflowMessage
			}
			if iNdEx >= l {
				return io.ErrUnexpectedEOF
			}
			b := dAtA[iNdEx]
			iNdEx++
			wire |= uint64(b&0x7F) << shift
			if b < 0x80 {
				break
			}
		}
		fieldNum := int32(wire >> 3)
		wireType := int(wire & 0x7)
		if wireType == 4 {
			return fmt.Errorf("proto: Message: wiretype end group for non-group")
		}
		if fieldNum <= 0 {
			return fmt.Errorf("proto: Message: illegal tag %d (wire type %d)", fieldNum, wire)
		}
		switch fieldNum {
		case 1:
			if wireType != 0 {
				return fmt.Errorf("proto: wrong wireType = %d for field Flag", wireType)
			}
			var v Message_Flag
			for shift := uint(0); ; shift += 7 {
				if shift >= 64 {
					return ErrIntOverflowMessage
				}
				if iNdEx >= l {
					return io.ErrUnexpectedEOF
				}
				b := dAtA[iNdEx]
				iNdEx++
				v |= Message_Flag(b&0x7F) << shift
				if b < 0x80 {
					break
				}
			}
			m.Flag = &v
		case 2:
			if wireType != 2 {
				return fmt.Errorf("proto: wrong wireType = %d for field Message", wireType)
			}
			var byteLen int
			for shift := uint(0); ; shift += 7 {
				if shift >= 64 {
					return ErrIntOverflowMessage
				}
				if iNdEx >= l {
					return io.ErrUnexpectedEOF
				}
				b := dAtA[iNdEx]
				iNdEx++
				byteLen |= int(b&0x7F) << shift
				if b < 0x80 {
					break
				}
			}
			if byteLen < 0 {
				return ErrInvalidLengthMessage
			}
			postIndex := iNdEx + byteLen
			if postIndex < 0 {
				return ErrInvalidLengthMessage
			}
			if postIndex > l {
				return io.ErrUnexpectedEOF
			}
			m.Message = append(m.Message[:0], dAtA[iNdEx:postIndex]...)
			if m.Message == nil {
				m.Message = []byte{}
			}
			iNdEx = postIndex
		default:
			iNdEx = preIndex
			skippy, err := skipMessage(dAtA[iNdEx:])
			if err != nil {
				return err
			}
			if (skippy < 0) || (iNdEx+skippy) < 0 {
				return ErrInvalidLengthMessage
			}
			if (iNdEx + skippy) > l {
				return io.ErrUnexpectedEOF
			}
			m.XXX_unrecognized = append(m.XXX_unrecognized, dAtA[iNdEx:iNdEx+skippy]...)
			iNdEx += skippy
		}
	}

	if iNdEx > l {
		return io.ErrUnexpectedEOF
	}
	return nil
}
func skipMessage(dAtA []byte) (n int, err error) {
	l := len(dAtA)
	iNdEx := 0
	depth := 0
	for iNdEx < l {
		var wire uint64
		for shift := uint(0); ; shift += 7 {
			if shift >= 64 {
				return 0, ErrIntOverflowMessage
			}
			if iNdEx >= l {
				return 0, io.ErrUnexpectedEOF
			}
			b := dAtA[iNdEx]
			iNdEx++
			wire |= (uint64(b) & 0x7F) << shift
			if b < 0x80 {
				break
			}
		}
		wireType := int(wire & 0x7)
		switch wireType {
		case 0:
			for shift := uint(0); ; shift += 7 {
				if shift >= 64 {
					return 0, ErrIntOverflowMessage
				}
				if iNdEx >= l {
					return 0, io.ErrUnexpectedEOF
				}
				iNdEx++
				if dAtA[iNdEx-1] < 0x80 {
					break
				}
			}
		case 1:
			iNdEx += 8
		case 2:
			var length int
			for shift := uint(0); ; shift += 7 {
				if shift >= 64 {
					return 0, ErrIntOverflowMessage
				}
				if iNdEx >= l {
					return 0, io.ErrUnexpectedEOF
				}
				b := dAtA[iNdEx]
				iNdEx++
				length |= (int(b) & 0x7F) << shift
				if b < 0x80 {
					break
				}
			}
			if length < 0 {
				return 0, ErrInvalidLengthMessage
			}
			iNdEx += length
		case 3:
			depth++
		case 4:
			if depth == 0 {
				return 0, ErrUnexpectedEndOfGroupMessage
			}
			depth--
		case 5:
			iNdEx += 4
		default:
			return 0, fmt.Errorf("proto: illegal wireType %d", wireType)
		}
		if iNdEx < 0 {
			return 0, ErrInvalidLengthMessage
		}
		if depth == 0 {
			return iNdEx, nil
		}
	}
	return 0, io.ErrUnexpectedEOF
}

var (
	ErrInvalidLengthMessage        = fmt.Errorf("proto: negative length found during unmarshaling")
	ErrIntOverflowMessage          = fmt.Errorf("proto: integer overflow")
	ErrUnexpectedEndOfGroupMessage = fmt.Errorf("proto: unexpected end of group")
)
