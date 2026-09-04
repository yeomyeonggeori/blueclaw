package policy

type PolicyDocument struct {
	People         []PersonPolicy         `json:"people"`
	Circles        []CirclePolicy         `json:"circles"`
	OrgGroups      []OrgGroupPolicy       `json:"orgGroups,omitempty"`
	Company        CompanyPolicy          `json:"company,omitempty"`
	Channels       []ChannelPolicy        `json:"channels"`
	ResourceAccess []ResourceAccessPolicy `json:"resourceAccess"`
	Retention      RetentionPolicy        `json:"retention"`
	Rules          []TopicRule            `json:"rules"`
	Metadata       PolicyMetadata         `json:"metadata"`
}

type PersonPolicy struct {
	PersonID          string   `json:"personID"`
	DisplayName       string   `json:"displayName"`
	Emails            []string `json:"emails"`
	Circles           []string `json:"circles"`
	SecurityLevelName string   `json:"securityLevelName"`
	SecurityLevelRank int      `json:"securityLevelRank"`
	GrantedClasses    []string `json:"grantedClasses"`
	IsAdmin           bool     `json:"isAdmin"`
	JobTitle          string   `json:"jobTitle,omitempty"`
	Group             string   `json:"group,omitempty"`
	SupervisorID      string   `json:"supervisorID,omitempty"`
}

type OrgGroupPolicy struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type CompanyPolicy struct {
	Name           string `json:"name,omitempty"`
	BrandName      string `json:"brandName,omitempty"`
	Slogan         string `json:"slogan,omitempty"`
	Description    string `json:"description,omitempty"`
	Representative string `json:"representative,omitempty"`
	Website        string `json:"website,omitempty"`
	TimeZone       string `json:"timeZone,omitempty"`
}

func (company CompanyPolicy) IsEmpty() bool {
	return company.Name == "" && company.BrandName == "" && company.Description == ""
}

type CirclePolicy struct {
	CircleID               string `json:"circleID"`
	DisplayName            string `json:"displayName"`
	MattermostChannelID    string `json:"mattermostChannelID,omitempty"`
	WorkspaceDirectoryPath string `json:"workspaceDirectoryPath,omitempty"`
}

type ChannelPolicy struct {
	Platform                 string   `json:"platform"`
	ExternalConversationID   string   `json:"externalConversationID"`
	ConversationType         string   `json:"conversationType"`
	DisplayName              string   `json:"displayName"`
	DefaultSecurityLevelRank int      `json:"defaultSecurityLevelRank"`
	DefaultRequiredClasses   []string `json:"defaultRequiredClasses"`
	IsCollectEnabled         bool     `json:"isCollectEnabled"`
	IsReplyEnabled           bool     `json:"isReplyEnabled"`
}

type ResourceAccessPolicy struct {
	Resource string   `json:"resource"`
	Actions  []string `json:"actions"`
	Circles  []string `json:"circles"`
}

type RetentionPolicy struct {
	RawEventDays int `json:"rawEventDays"`
}

type TopicRule struct {
	Name                string   `json:"name"`
	TopicKeywords       []string `json:"topicKeywords"`
	RequiredClasses     []string `json:"requiredClasses"`
	MinimumSecurityRank int      `json:"minimumSecurityRank"`
}

type PolicyMetadata struct {
	Version int    `json:"version"`
	Author  string `json:"author"`
}
