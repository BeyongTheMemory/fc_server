package vo

type AddressLevel uint16

const (
	Unknown AddressLevel = iota
	Province
	City
	District
)

// GetLocationValue 根据地址级别获取对应的location值
func (level AddressLevel) GetLocationValue(location *Location) string {
	switch level {
	case Province:
		return location.Province
	case City:
		return location.City
	case District:
		return location.District
	default:
		return ""
	}
}

// GetAllLevels 获取所有地址级别
func GetAllLevels() []AddressLevel {
	return []AddressLevel{Province, City, District}
}
