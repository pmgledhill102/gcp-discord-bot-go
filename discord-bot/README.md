# Discord Bot

An example function for comparing eperformance of Discord Bots across multiple languages

```go
type InteractionType uint8

// Interaction types
const (
	InteractionPing                           InteractionType = 1
	InteractionApplicationCommand             InteractionType = 2
	InteractionMessageComponent               InteractionType = 3
	InteractionApplicationCommandAutocomplete InteractionType = 4
	InteractionModalSubmit                    InteractionType = 5
)

// InteractionResponse represents a response for an interaction event.
type InteractionResponse struct {
	Type InteractionResponseType  `json:"type,omitempty"`
	Data *InteractionResponseData `json:"data,omitempty"`
}

// InteractionResponseData is response data for an interaction.
type InteractionResponseData struct {
	TTS             bool                    `json:"tts"`
	Content         string                  `json:"content"`
	Components      []MessageComponent      `json:"components"`
	Embeds          []*MessageEmbed         `json:"embeds"`
	AllowedMentions *MessageAllowedMentions `json:"allowed_mentions,omitempty"`
	Files           []*File                 `json:"-"`

	// NOTE: only MessageFlagsSuppressEmbeds and MessageFlagsEphemeral can be set.
	Flags MessageFlags `json:"flags,omitempty"`

	// NOTE: autocomplete interaction only.
	Choices []*ApplicationCommandOptionChoice `json:"choices,omitempty"`

	// NOTE: modal interaction only.

	CustomID string `json:"custom_id,omitempty"`
	Title    string `json:"title,omitempty"`
}

// InteractionResponseType is type of interaction response.
type InteractionResponseType uint8

// Interaction response types.
const (
	InteractionResponsePong                             InteractionResponseType = 1
	InteractionResponseChannelMessageWithSource         InteractionResponseType = 4
	InteractionResponseDeferredChannelMessageWithSource InteractionResponseType = 5
	InteractionResponseDeferredMessageUpdate            InteractionResponseType = 6
	InteractionResponseUpdateMessage                    InteractionResponseType = 7
	InteractionApplicationCommandAutocompleteResult     InteractionResponseType = 8
	InteractionResponseModal                            InteractionResponseType = 9
    InteractionPremiumRequired                          InteractionResponseType = 10
)

/ Interaction represents data of an interaction.
type Interaction struct {
	ID        string          `json:"id"`
	AppID     string          `json:"application_id"`
	Type      InteractionType `json:"type"`
	Data      InteractionData `json:"data"`
	GuildID   string          `json:"guild_id"`
	ChannelID string          `json:"channel_id"`

	// The message on which interaction was used.
	// NOTE: this field is only filled when a button click triggered the interaction. Otherwise it will be nil.
	
    //####PMG - Message *Message `json:"message"`

	// Bitwise set of permissions the app or bot has within the channel the interaction was sent from
	AppPermissions int64 `json:"app_permissions,string"`

	// The member who invoked this interaction.
	// NOTE: this field is only filled when the slash command was invoked in a guild;
	// if it was invoked in a DM, the `User` field will be filled instead.
	// Make sure to check for `nil` before using this field.
	Member *Member `json:"member"`
	// The user who invoked this interaction.
	// NOTE: this field is only filled when the slash command was invoked in a DM;
	// if it was invoked in a guild, the `Member` field will be filled instead.
	// Make sure to check for `nil` before using this field.
	User *User `json:"user"`

	// The user's discord client locale.
	Locale Locale `json:"locale"`
	// The guild's locale. This defaults to EnglishUS
	// NOTE: this field is only filled when the interaction was invoked in a guild.
	§GuildLocale *Locale `json:"guild_locale"`

	Token   string `json:"token"`
	Version int    `json:"version"`
}


// A User stores all data for an individual Discord user.
type User struct {
	// The ID of the user.
	ID string `json:"id"`

	// The email of the user. This is only present when
	// the application possesses the email scope for the user.
	Email string `json:"email"`

	// The user's username.
	Username string `json:"username"`

	// The hash of the user's avatar. Use Session.UserAvatar
	// to retrieve the avatar itself.
	Avatar string `json:"avatar"`

	// The user's chosen language option.
	Locale string `json:"locale"`

	// The discriminator of the user (4 numbers after name).
	Discriminator string `json:"discriminator"`

	// The user's display name, if it is set.
	// For bots, this is the application name.
	GlobalName string `json:"global_name"`

	// The token of the user. This is only present for
	// the user represented by the current session.
	Token string `json:"token"`

	// Whether the user's email is verified.
	Verified bool `json:"verified"`

	// Whether the user has multi-factor authentication enabled.
	MFAEnabled bool `json:"mfa_enabled"`

	// The hash of the user's banner image.
	Banner string `json:"banner"`

	// User's banner color, encoded as an integer representation of hexadecimal color code
	AccentColor int `json:"accent_color"`

	// Whether the user is a bot.
	Bot bool `json:"bot"`

	// The public flags on a user's account.
	// This is a combination of bit masks; the presence of a certain flag can
	// be checked by performing a bitwise AND between this int and the flag.
	//### PMG - PublicFlags UserFlags `json:"public_flags"`

	// The type of Nitro subscription on a user's account.
	// Only available when the request is authorized via a Bearer token.
	//### PMG - PremiumType UserPremiumType `json:"premium_type"`

	// Whether the user is an Official Discord System user (part of the urgent message system).
	System bool `json:"system"`

	// The flags on a user's account.
	// Only available when the request is authorized via a Bearer token.
	Flags int `json:"flags"`
}

// A Member stores user information for Guild members. A guild
// member represents a certain user's presence in a guild.
type Member struct {
	// The guild ID on which the member exists.
	GuildID string `json:"guild_id"`

	// The time at which the member joined the guild.
	JoinedAt time.Time `json:"joined_at"`

	// The nickname of the member, if they have one.
	Nick string `json:"nick"`

	// Whether the member is deafened at a guild level.
	Deaf bool `json:"deaf"`

	// Whether the member is muted at a guild level.
	Mute bool `json:"mute"`

	// The hash of the avatar for the guild member, if any.
	Avatar string `json:"avatar"`

	// The underlying user on which the member is based.
	User *User `json:"user"`

	// A list of IDs of the roles which are possessed by the member.
	Roles []string `json:"roles"`

	// When the user used their Nitro boost on the server
	PremiumSince *time.Time `json:"premium_since"`

	// Is true while the member hasn't accepted the membership screen.
	Pending bool `json:"pending"`

	// Total permissions of the member in the channel, including overrides, returned when in the interaction object.
	Permissions int64 `json:"permissions,string"`

	// The time at which the member's timeout will expire.
	// Time in the past or nil if the user is not timed out.
	CommunicationDisabledUntil *time.Time `json:"communication_disabled_until"`
}

type ApplicationCommandOptionChoice struct {
	Name              string            `json:"name"`
	NameLocalizations map[Locale]string `json:"name_localizations,omitempty"`
	Value             interface{}       `json:"value"`
}

type Locale string
```



body.type

// Discord enumerations
const InteractionResponseType = {
    Pong: 1,
    ChannelMessageWithSource: 4,
    DeferredChannelMessageWithSource: 5
  };

const InteractionType = {
    Ping: 1,
    ApplicationCommand: 2
};

    switch (body.type) {

        case InteractionType.Ping:
        "body": JSON.stringify({"type": InteractionResponseType.Pong})

        // === Normal Response
        case InteractionType.ApplicationCommand:

            // Build command text
            //
            // To convert the structure to a simple string seems like the simplest way, starts as 
            /*  "data": {
                    "id": "798162069974417438",
                    "name": "dfc",
                    "options": [{
                        "name": "server",
                        "options": [{
                            "name": "start",
                            "options": [{
                                "name": "game",
                                "value": "csgo"
                            }]
                        }]
                    }]
                }
            */

                cmdText = `${body.data.options[0].name} ${body.data.options[0].options[0].name}`

                        "type": InteractionResponseType.ChannelMessageWithSource,

                    // DFC SERVER START / STOP / RESTART
                    case "server start":
                    case "server stop":
                    case "server restart":

                        var gameServer = body.data.options[0].options[0].options[0].value;
                        let op = `${body.data.options[0].options[0].name}`;
                    case "server info":

                        var gameServer = body.data.options[0].options[0].options[0].value;
                            "type": InteractionResponseType.ChannelMessageWithSource,
                            "data": returnData

                    {
                        "type": InteractionResponseType.ChannelMessageWithSource,
                        "data": { "content": `Error thrown whilst trying to execute command '${cmdText}'` }
                    });
            }
            break;

