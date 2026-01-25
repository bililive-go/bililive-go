package configs

// MergeStreamPreference 深度合并流偏好
// child 的非nil字段会覆盖 parent 的对应字段
// Attributes 采用深度合并：child 的 key 覆盖 parent 的 key，空字符串表示移除该key
func MergeStreamPreference(parent, child *StreamPreference) *StreamPreference {
	if child == nil {
		return parent
	}
	if parent == nil {
		return child
	}

	merged := &StreamPreference{}

	// 合并 Quality
	if child.Quality != nil {
		merged.Quality = child.Quality
	} else if parent.Quality != nil {
		merged.Quality = parent.Quality
	}

	// 合并 Attributes（深度合并）
	if parent.Attributes != nil || child.Attributes != nil {
		attrs := make(map[string]string)

		// 先复制 parent 的 attributes
		if parent.Attributes != nil {
			for k, v := range *parent.Attributes {
				attrs[k] = v
			}
		}

		// child 的 attributes 覆盖
		if child.Attributes != nil {
			for k, v := range *child.Attributes {
				if v == "" {
					// 空字符串表示"移除这个属性"
					delete(attrs, k)
				} else {
					attrs[k] = v
				}
			}
		}

		if len(attrs) > 0 {
			merged.Attributes = &attrs
		}
	}

	return merged
}
