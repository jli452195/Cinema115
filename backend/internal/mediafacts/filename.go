package mediafacts

import (
	"path"
	"strconv"
	"strings"
)

// FilenameFacts 在一次提取中保留作品声明与标准坐标，供自动整理复用。
type FilenameFacts struct {
	Identity   Identity
	Coordinate CoordinateClaim
}

// ParseFilename 按坐标原文位置截取作品部分，分集标题不再提供作品名或年份。
func ParseFilename(name string) FilenameFacts {
	stem := strings.TrimSuffix(name, path.Ext(name))
	coordinate := ParseStandardCoordinates(stem)
	if !coordinate.Found {
		return FilenameFacts{Identity: ParseIdentity(name), Coordinate: coordinate}
	}
	// 补一个固定扩展名，避免片名中的点号被旧身份解析当成文件扩展名。
	identity := ParseIdentity(stem[:coordinate.End] + ".mediafacts")
	for _, match := range identityID.FindAllStringSubmatch(stem, -1) {
		id, _ := strconv.Atoi(match[1])
		if identity.TMDBID == 0 {
			identity.TMDBID, identity.TMDBIDSource = id, "filename"
		} else if id > 0 && id != identity.TMDBID {
			identityAddConflict(&identity, "TMDB_ID_CONFLICT")
		}
	}
	identityContentDeclaration(stem, "filename", &identity)
	if coordinate.Valid {
		identity.Kind, identity.KindSource = "tv", "filename"
		identity.Season, identity.SeasonExplicit, identity.SeasonSource = coordinate.Numbering.Season, true, "filename"
	}
	return FilenameFacts{Identity: identity, Coordinate: coordinate}
}
