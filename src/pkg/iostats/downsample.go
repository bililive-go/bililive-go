package iostats

import "strings"

const (
	defaultRawBucketMs    int64 = 5 * 1000
	targetPointsPerSeries       = 1200
)

var supportedBucketMs = []int64{
	5 * 1000,
	10 * 1000,
	15 * 1000,
	30 * 1000,
	60 * 1000,
	2 * 60 * 1000,
	5 * 60 * 1000,
	10 * 60 * 1000,
	15 * 60 * 1000,
	30 * 60 * 1000,
	60 * 60 * 1000,
	2 * 60 * 60 * 1000,
	6 * 60 * 60 * 1000,
	12 * 60 * 60 * 1000,
	24 * 60 * 60 * 1000,
}

type downsamplePlan struct {
	requested string
	applied   string
	bucketMs  int64
}

func buildDownsamplePlan(startTime, endTime int64, requested string) downsamplePlan {
	requested = normalizeAggregation(requested)
	span := endTime - startTime
	if span < 0 {
		span = 0
	}

	requestedBucketMs := aggregationToBucketMs(requested)
	autoBucketMs := autoBucketMs(span)

	bucketMs := requestedBucketMs
	applied := requested

	if autoBucketMs > bucketMs {
		bucketMs = autoBucketMs
		applied = "auto"
	}

	if bucketMs == 0 {
		applied = "raw"
	}

	return downsamplePlan{
		requested: requested,
		applied:   applied,
		bucketMs:  bucketMs,
	}
}

func normalizeAggregation(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "auto":
		return "auto"
	case "none", "raw":
		return "raw"
	case "minute":
		return "minute"
	case "hour":
		return "hour"
	default:
		return "auto"
	}
}

func aggregationToBucketMs(value string) int64 {
	switch normalizeAggregation(value) {
	case "minute":
		return 60 * 1000
	case "hour":
		return 3600 * 1000
	default:
		return 0
	}
}

func autoBucketMs(spanMs int64) int64 {
	if spanMs <= 0 {
		return 0
	}

	if ceilDiv(spanMs, defaultRawBucketMs) <= targetPointsPerSeries {
		return 0
	}

	for _, bucketMs := range supportedBucketMs {
		if ceilDiv(spanMs, bucketMs) <= targetPointsPerSeries {
			return bucketMs
		}
	}

	return supportedBucketMs[len(supportedBucketMs)-1]
}

func ceilDiv(value, divisor int64) int {
	if divisor <= 0 {
		return 0
	}
	return int((value + divisor - 1) / divisor)
}

func globalOnlyStatType(statType StatType) bool {
	return statType == StatTypeNetworkDownload
}

func allQueryableStatTypes() []StatType {
	return []StatType{
		StatTypeNetworkDownload,
		StatTypeDiskRecordWrite,
		StatTypeDiskFixRead,
		StatTypeDiskFixWrite,
		StatTypeDiskConvertRead,
		StatTypeDiskConvertWrite,
	}
}
