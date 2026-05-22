package streamer

import (
	"context"
	"fmt"

	"github.com/gotd/td/tg"
)

type FileMeta struct {
	MessageID int64
	Location  tg.InputFileLocationClass
	Size      int64
	MimeType  string
	FileName  string
	UniqueID  string
	DC        int // Telegram DC where the file is stored (1-5)
}

func GetMeta(ctx context.Context, api *tg.Client, channelID int64, accessHash int64, messageID int) (*FileMeta, error) {
	channel := &tg.InputChannel{ChannelID: channelID, AccessHash: accessHash}
	res, err := api.ChannelsGetMessages(ctx, &tg.ChannelsGetMessagesRequest{
		Channel: channel,
		ID:      []tg.InputMessageClass{&tg.InputMessageID{ID: messageID}},
	})
	if err != nil {
		return nil, fmt.Errorf("get message %d: %w", messageID, err)
	}

	var msgs []tg.MessageClass
	switch m := res.(type) {
	case *tg.MessagesMessages:
		msgs = m.Messages
	case *tg.MessagesMessagesSlice:
		msgs = m.Messages
	case *tg.MessagesChannelMessages:
		msgs = m.Messages
	default:
		return nil, fmt.Errorf("unexpected messages type %T", res)
	}

	for _, mc := range msgs {
		msg, ok := mc.(*tg.Message)
		if !ok {
			continue
		}
		meta, err := metaFromMessage(msg)
		if err != nil {
			return nil, err
		}
		if meta != nil {
			return meta, nil
		}
	}
	return nil, fmt.Errorf("file not found in message %d", messageID)
}

func MetaFromMessage(msg *tg.Message) (*FileMeta, error) {
	return metaFromMessage(msg)
}

func metaFromMessage(msg *tg.Message) (*FileMeta, error) {
	media, ok := msg.GetMedia()
	if !ok {
		return nil, nil
	}

	switch m := media.(type) {
	case *tg.MessageMediaDocument:
		docClass, ok := m.GetDocument()
		if !ok {
			return nil, nil
		}
		doc, ok := docClass.(*tg.Document)
		if !ok {
			return nil, nil
		}
		fm := &FileMeta{
			MessageID: int64(msg.ID),
			Size:      doc.Size,
			MimeType:  doc.MimeType,
			DC:        doc.DCID,
			Location: &tg.InputDocumentFileLocation{
				ID:            doc.ID,
				AccessHash:    doc.AccessHash,
				FileReference: doc.FileReference,
				ThumbSize:     "",
			},
			UniqueID: uniqueIDFromDoc(doc),
		}
		for _, attr := range doc.Attributes {
			if a, ok := attr.(*tg.DocumentAttributeFilename); ok {
				fm.FileName = a.FileName
			}
		}
		return fm, nil

	case *tg.MessageMediaPhoto:
		photoClass, ok := m.GetPhoto()
		if !ok {
			return nil, nil
		}
		photo, ok := photoClass.(*tg.Photo)
		if !ok {
			return nil, nil
		}
		var size int64
		var thumbType string
		for _, s := range photo.Sizes {
			if ps, ok := s.(*tg.PhotoSize); ok {
				if int64(ps.Size) > size {
					size = int64(ps.Size)
					thumbType = ps.Type
				}
			}
		}
		return &FileMeta{
			MessageID: int64(msg.ID),
			Size:      size,
			MimeType:  "image/jpeg",
			Location: &tg.InputPhotoFileLocation{
				ID:            photo.ID,
				AccessHash:    photo.AccessHash,
				FileReference: photo.FileReference,
				ThumbSize:     thumbType,
			},
			UniqueID: uniqueIDFromPhoto(photo),
		}, nil
	}
	return nil, nil
}

func uniqueIDFromDoc(d *tg.Document) string {
	return fmt.Sprintf("%016x", uint64(d.ID))
}

func uniqueIDFromPhoto(p *tg.Photo) string {
	return fmt.Sprintf("%016x", uint64(p.ID))
}
