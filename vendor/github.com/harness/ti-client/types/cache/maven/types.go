package maven

import "encoding/xml"

type CacheReport struct {
	XMLName  xml.Name  `xml:"cacheReport"`
	Projects []Project `xml:"projects>project"`
}

type Project struct {
	GroupID          string `xml:"groupId"`
	ArtifactID       string `xml:"artifactId"`
	Checksum         string `xml:"checksum"`
	ChecksumMatched  bool   `xml:"checksumMatched"`
	LifecycleMatched bool   `xml:"lifecycleMatched"`
	Source           string `xml:"source"`
	SharedToRemote   bool   `xml:"sharedToRemote"`
	URL              string `xml:"url"`
}

type MavenMetrics struct {
	Reports []CacheReport `json:"cacheReport"`
}
