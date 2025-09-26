package flv

import "context"

func (p *Parser) parseTag(ctx context.Context) error {
	p.tagCount += 1

	// Check for stop signal before starting tag parsing
	select {
	case <-p.stopCh:
		return nil
	default:
	}

	b, err := p.i.ReadN(15)
	if err != nil {
		// If we encounter an error while reading tag header, it might be due to 
		// incomplete stream data. Check if we're being stopped gracefully.
		select {
		case <-p.stopCh:
			return nil // Graceful stop, ignore the error
		default:
			return err // Actual error, propagate it
		}
	}

	tagType := uint8(b[4])
	length := uint32(b[5])<<16 | uint32(b[6])<<8 | uint32(b[7])
	timeStamp := uint32(b[8])<<16 | uint32(b[9])<<8 | uint32(b[10]) | uint32(b[11])<<24

	switch tagType {
	case audioTag:
		if _, err := p.parseAudioTag(ctx, length, timeStamp); err != nil {
			// Check for stop signal in case of error during audio tag parsing
			select {
			case <-p.stopCh:
				return nil
			default:
				return err
			}
		}
	case videoTag:
		if _, err := p.parseVideoTag(ctx, length, timeStamp); err != nil {
			// Check for stop signal in case of error during video tag parsing  
			select {
			case <-p.stopCh:
				return nil
			default:
				return err
			}
		}
	case scriptTag:
		if err := p.parseScriptTag(ctx, length); err != nil {
			// Check for stop signal in case of error during script tag parsing
			select {
			case <-p.stopCh:
				return nil
			default:
				return err
			}
		}
	default:
		return ErrUnknownTag
	}

	return nil
}
