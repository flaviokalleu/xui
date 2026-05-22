package botapi

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"io"
	"math/big"
	"os"

	"github.com/gotd/td/tg"
	"syncgo/internal/telegram"
)

const mtprotoPartSize = 512 * 1024 // 512 KB — tamanho de chunk exigido pelo Telegram MTProto

// ResolveChannelPeer resolve o InputPeer de um canal via MTProto.
// logChannelID é o ID estilo Bot API (ex: -1001234567890).
// Tenta channels.getChannels (access_hash=0, funciona para bots admin) antes de
// varrer os dialogs como fallback.
func ResolveChannelPeer(ctx context.Context, api *tg.Client, logChannelID int64) (tg.InputPeerClass, error) {
	// Converte ID Bot API (-100XXXXXXXXX) para ID MTProto (XXXXXXXXX)
	channelID := -logChannelID - 1_000_000_000_000

	// 1. Tenta channels.getChannels com access_hash=0 — bots admin sempre conseguem
	chansResult, err := api.ChannelsGetChannels(ctx, []tg.InputChannelClass{
		&tg.InputChannel{ChannelID: channelID, AccessHash: 0},
	})
	if err == nil {
		var chats []tg.ChatClass
		switch v := chansResult.(type) {
		case *tg.MessagesChats:
			chats = v.Chats
		case *tg.MessagesChatsSlice:
			chats = v.Chats
		}
		for _, chat := range chats {
			if ch, ok := chat.(*tg.Channel); ok && ch.ID == channelID {
				return &tg.InputPeerChannel{
					ChannelID:  ch.ID,
					AccessHash: ch.AccessHash,
				}, nil
			}
		}
	}

	// 2. Fallback: varre dialogs (até 200)
	result, err := api.MessagesGetDialogs(ctx, &tg.MessagesGetDialogsRequest{
		Limit:      200,
		OffsetPeer: &tg.InputPeerEmpty{},
	})
	if err != nil {
		return nil, fmt.Errorf("get dialogs: %w", err)
	}

	for _, chat := range extractChats(result) {
		if ch, ok := chat.(*tg.Channel); ok && ch.ID == channelID {
			return &tg.InputPeerChannel{
				ChannelID:  ch.ID,
				AccessHash: ch.AccessHash,
			}, nil
		}
	}
	return nil, fmt.Errorf("canal %d não encontrado — o bot é admin do canal?", logChannelID)
}

func extractChats(result tg.MessagesDialogsClass) []tg.ChatClass {
	switch d := result.(type) {
	case *tg.MessagesDialogs:
		return d.Chats
	case *tg.MessagesDialogsSlice:
		return d.Chats
	case *tg.MessagesDialogsNotModified:
		return nil
	}
	return nil
}

// UploadDocumentMTProto faz download do arquivo para disco e envia ao canal via MTProto.
// Sem limite de 50 MB — usa upload.saveBigFilePart em chunks de 512 KB.
// Retorna o MessageID da mensagem postada no canal.
func UploadDocumentMTProto(ctx context.Context, pool *telegram.Pool, peer tg.InputPeerClass, fileName string, r io.Reader, onProgress func(sent, total int64)) (int, error) {
	cli := pool.Main
	if cli == nil || cli.API == nil {
		return 0, fmt.Errorf("cliente MTProto não está pronto")
	}

	// 1. Baixar para arquivo temporário (necessário para saber o total de partes)
	tmp, err := os.CreateTemp("", "syncgo-upload-*.tmp")
	if err != nil {
		return 0, fmt.Errorf("criar temp: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	written, err := io.Copy(tmp, r)
	tmp.Close()
	if err != nil {
		return 0, fmt.Errorf("download para disco: %w", err)
	}

	// 2. Upload em chunks via MTProto
	fileID, err := randomInt63()
	if err != nil {
		return 0, err
	}

	totalParts := int((written + mtprotoPartSize - 1) / mtprotoPartSize)
	isBig := written > 10*1024*1024

	f, err := os.Open(tmpPath)
	if err != nil {
		return 0, err
	}
	defer f.Close()

	buf := make([]byte, mtprotoPartSize)
	var sent int64
	for partNum := 0; partNum < totalParts; partNum++ {
		n, err := io.ReadFull(f, buf)
		if n == 0 {
			break
		}
		if err != nil && err != io.ErrUnexpectedEOF {
			return 0, fmt.Errorf("leitura parte %d: %w", partNum, err)
		}

		data := buf[:n]
		if isBig {
			if _, err := cli.API.UploadSaveBigFilePart(ctx, &tg.UploadSaveBigFilePartRequest{
				FileID:         fileID,
				FilePart:       partNum,
				FileTotalParts: totalParts,
				Bytes:          data,
			}); err != nil {
				return 0, fmt.Errorf("saveBigFilePart %d: %w", partNum, err)
			}
		} else {
			if _, err := cli.API.UploadSaveFilePart(ctx, &tg.UploadSaveFilePartRequest{
				FileID:   fileID,
				FilePart: partNum,
				Bytes:    data,
			}); err != nil {
				return 0, fmt.Errorf("saveFilePart %d: %w", partNum, err)
			}
		}

		sent += int64(n)
		if onProgress != nil {
			onProgress(sent, written)
		}
	}

	// 3. Montar InputFile
	var inputFile tg.InputFileClass
	if isBig {
		inputFile = &tg.InputFileBig{
			ID:    fileID,
			Parts: totalParts,
			Name:  fileName,
		}
	} else {
		inputFile = &tg.InputFile{
			ID:    fileID,
			Parts: totalParts,
			Name:  fileName,
		}
	}

	// 4. Enviar ao canal
	randomID, err := randomInt63()
	if err != nil {
		return 0, err
	}

	updates, err := cli.API.MessagesSendMedia(ctx, &tg.MessagesSendMediaRequest{
		Peer: peer,
		Media: &tg.InputMediaUploadedDocument{
			File:     inputFile,
			MimeType: mimeFromName(fileName),
			Attributes: []tg.DocumentAttributeClass{
				&tg.DocumentAttributeFilename{FileName: fileName},
			},
		},
		RandomID: randomID,
		Message:  "",
	})
	if err != nil {
		return 0, fmt.Errorf("send media: %w", err)
	}

	msgID := extractMsgID(updates)
	if msgID == 0 {
		return 0, fmt.Errorf("não foi possível extrair MessageID das updates")
	}
	return msgID, nil
}

func extractMsgID(updates tg.UpdatesClass) int {
	switch u := updates.(type) {
	case *tg.Updates:
		for _, upd := range u.Updates {
			switch m := upd.(type) {
			case *tg.UpdateNewChannelMessage:
				if msg, ok := m.Message.(*tg.Message); ok {
					return msg.ID
				}
			case *tg.UpdateNewMessage:
				if msg, ok := m.Message.(*tg.Message); ok {
					return msg.ID
				}
			case *tg.UpdateMessageID:
				return m.ID
			}
		}
	case *tg.UpdateShortSentMessage:
		return u.ID
	}
	return 0
}

func mimeFromName(name string) string {
	for i := len(name) - 1; i >= 0; i-- {
		if name[i] == '.' {
			switch name[i+1:] {
			case "mkv":
				return "video/x-matroska"
			case "avi":
				return "video/x-msvideo"
			case "ts":
				return "video/mp2t"
			case "m4v":
				return "video/x-m4v"
			}
			return "video/mp4"
		}
	}
	return "video/mp4"
}

func randomInt63() (int64, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(1<<62))
	if err != nil {
		var b [8]byte
		binary.Read(rand.Reader, binary.LittleEndian, &b)
		return int64(binary.LittleEndian.Uint64(b[:])) & (1<<62 - 1), nil
	}
	return n.Int64(), err
}
