package discord

import (
	"github.com/bwmarrin/discordgo"
	"github.com/Clinet/clinet_cmds"
	"github.com/Clinet/clinet_features"
	"github.com/Clinet/clinet_services"
	"github.com/Clinet/clinet_storage"
	"github.com/Clinet/discordgo-embed"
	"github.com/JoshuaDoes/logger"
)

var Feature = features.Feature{
	Name: "discord",
	ServiceChat: &ClientDiscord{},
}
var Discord *ClientDiscord

var Log *logger.Logger
func init() {
	Log = logger.NewLogger("discord", 2)
}

//ClientDiscord implements services.Service and holds a Discord session
type ClientDiscord struct {
	*discordgo.Session
	User *discordgo.User
	VCs  []*discordgo.VoiceConnection
	Cfg  *storage.Storage
	Storage *storage.Storage
}

func (discord *ClientDiscord) Shutdown() {
	for _, vc := range discord.VCs {
		_ = vc.Disconnect()
	}
	_ = discord.Close()
}

func (discord *ClientDiscord) CmdPrefix() string {
	return "/"
}

func (discord *ClientDiscord) Login() (err error) {
	Log.Trace("--- StartDiscord() ---")
	cfg := &storage.Storage{}
	if err := cfg.LoadFrom("discord"); err != nil {
		return services.Error("discord: Unable to read config: %v", err)
	}
	token, err := cfg.ConfigGet("cfg", "token")
	if err != nil {
		return services.Error("discord: Unable to read cfg:token from storage: %v", err)
	}

	state := &storage.Storage{}
	if err := state.LoadFrom("discordstate"); err != nil {
		return services.Error("discord: Unable to load state: %v", err)
	}

	Log.Debug("Creating Discord struct...")
	discordClient, err := discordgo.New("Bot " + token.(string))
	if err != nil {
		return services.Error("discord: Unable to create bot instance: %v", err)
	}

	Log.Info("Registering Discord event handlers...")
	discordClient.AddHandler(discordReady)
	discordClient.AddHandler(discordMessageCreate)
	discordClient.AddHandler(discordInteractionCreate)

	Log.Info("Connecting to Discord...")
	err = discordClient.Open()
	if err != nil {
		return services.Error("discord: Unable to connect to Discord: %v", err)
	}

	Log.Info("Connected to Discord!")
	Discord = &ClientDiscord{discordClient, nil, make([]*discordgo.VoiceConnection, 0), cfg, state}
	discord = Discord

	Log.Info("Registering application commands...")
	_, err = discord.ApplicationCommandBulkOverwrite(discord.State.User.ID, "", CmdsToAppCommands())
	if err != nil {
		return services.Error("discord: Unable to overwrite commands: %v", err)
	}

	Log.Info("Syncing application commands...")
	appCmds, err := discord.ApplicationCommands(discord.State.User.ID, "")
	if err != nil {
		return services.Error("discord: Unable to retrieve commands: %v", err)
	}

	for _, appCmd := range appCmds {
		if cmds.GetCmd(appCmd.Name) == nil {
			Log.Warn("Deleting old command: " + appCmd.Name)
			err = discord.ApplicationCommandDelete(discord.State.User.ID, "", appCmd.ID)
			if err != nil {
				return services.Error("discord: Unable to delete old command %s: %v", appCmd.Name, err)
			}
		}
	}

	Log.Info("Application commands ready for use!")
	return nil
}

func (discord *ClientDiscord) MsgEdit(msg *services.Message) (ret *services.Message, err error) {
	return nil, nil
}
func (discord *ClientDiscord) MsgRemove(msg *services.Message) (err error) {
	return nil
}
func (discord *ClientDiscord) MsgSend(msg *services.Message, ref interface{}) (ret *services.Message, err error) {
	msgContext := msg.Context
	switch msgContext.(type) {
	case *discordgo.Message:
		if msg.ChannelID == "" {
			return nil, services.Error("discord: MsgSend(msg: %v): missing channel ID", msg)
		}
	case *discordgo.Interaction:
		if msg.Context == nil {
			return nil, services.Error("discord: MsgSend(msg: %v): missing interaction as context", msg)
		}
	default:
		//Sending a DM to a user should always be a regular message
		if msg.ServerID == "" && msg.ChannelID != "" {
			channelDM, err := discord.UserChannelCreate(msg.ChannelID)
			if err != nil {
				return nil, services.Error("discord: MsgSend(msg: %v): unable to create DM with userID: %s: %v", msg, msg.ChannelID, err)
			}
			msg.ChannelID = channelDM.ID
		} else {
			return nil, services.Error("discord: MsgSend(msg: %v): unknown MsgContext: %d", msg, msgContext)
		}
	}

	var discordMsg *discordgo.Message
	if msg.Title != "" || msg.Color > 0 || msg.Image != "" {
		retEmbed := embed.NewEmbed().SetDescription(msg.Content)
		if msg.Title != "" {
			retEmbed.SetTitle(msg.Title)
		}
		if msg.Color > 0 {
			retEmbed.SetColor(msg.Color)
		}
		if msg.Image != "" {
			retEmbed.SetImage(msg.Image)
		}
		if len(msg.Fields) > 0 {
			for i := 0; i < len(msg.Fields); i++ {
				retEmbed.AddField(msg.Fields[i].Name, msg.Fields[i].Value)
			}
		}

		switch msgContext.(type) {
		case *discordgo.Interaction:
			interactionResp := &discordgo.InteractionResponse{
				Type: discordgo.InteractionResponseChannelMessageWithSource,
				Data: &discordgo.InteractionResponseData{
					Embeds: []*discordgo.MessageEmbed{retEmbed.MessageEmbed},
				},
			}
			interaction := msg.Context.(*discordgo.Interaction)
			err = discord.InteractionRespond(interaction, interactionResp)
		default:
			discordMsg, err = discord.ChannelMessageSendComplex(msg.ChannelID, &discordgo.MessageSend{Embed: retEmbed.MessageEmbed})
		}
	} else {
		if msg.Content == "" {
			return nil, services.Error("discord: MsgSend(msg: %v): missing content", msg)
		}

		switch msgContext.(type) {
		case *discordgo.Interaction:
			interactionResp := &discordgo.InteractionResponse{
				Type: discordgo.InteractionResponseChannelMessageWithSource,
				Data: &discordgo.InteractionResponseData{
					Content: msg.Content,
				},
			}
			interaction := msg.Context.(*discordgo.Interaction)
			err = discord.InteractionRespond(interaction, interactionResp)
		default:
			discordMsgSend := &discordgo.MessageSend{Content: msg.Content}
			if ref != nil {
				discordMsgSend.Reference = ref.(*discordgo.MessageReference)
			}
			discordMsg, err = discord.ChannelMessageSendComplex(msg.ChannelID, discordMsgSend)
		}
	}
	if err != nil {
		return nil, err
	}

	ret = msg
	if discordMsg != nil {
		ret.UserID = discordMsg.Author.ID
		ret.ServerID = discordMsg.GuildID
	}
	if discordMsg != nil {
		ret = &services.Message{
			UserID: discordMsg.Author.ID,
			MessageID: discordMsg.ID,
			ChannelID: discordMsg.ChannelID,
			ServerID: discordMsg.GuildID,
			Content: discordMsg.Content,
			Context: discordMsg,
		}
	}
	return ret, err
}

func (discord *ClientDiscord) GetUser(serverID, userID string) (ret *services.User, err error) {
	user, err := discord.GuildMember(serverID, userID)
	if err != nil {
		return nil, err
	}

	userRoles := make([]*services.Role, len(user.Roles))
	for i := 0; i < len(userRoles); i++ {
		role := &services.Role{
			RoleID: user.Roles[i],
		}
		userRoles[i] = role
	}
	return &services.User{
		ServerID: serverID,
		UserID: userID,
		Username: user.User.Username,
		Nickname: user.Nick,
		Roles: userRoles,
	}, nil
}
func (discord *ClientDiscord) GetUserPerms(serverID, channelID, userID string) (perms *services.Perms, err error) {
	user, err := discord.GetUser(serverID, userID)
	if err != nil {
		return nil, err
	}

	server, err := discord.GetServer(serverID)
	if err != nil {
		return nil, err
	}

	guildRoles, err := discord.GuildRoles(serverID)
	if err != nil {
		return nil, err
	}

	channel, err := discord.Channel(channelID)
	if err != nil {
		return nil, err
	}

	perms = &services.Perms{}
	for i := 0; i < len(guildRoles); i++ {
		for j := 0; j < len(user.Roles); j++ {
			if guildRoles[i].ID == user.Roles[j].RoleID {
				guildRolePerms := guildRoles[i].Permissions
				if guildRolePerms & discordgo.PermissionAdministrator != 0 {
					perms.Administrator = true
				}
				if guildRolePerms & discordgo.PermissionKickMembers != 0 {
					perms.Kick = true
				}
				if guildRolePerms & discordgo.PermissionBanMembers != 0 {
					perms.Ban = true
				}

				for _, overwrite := range channel.PermissionOverwrites {
					if overwrite.Type == discordgo.PermissionOverwriteTypeRole && overwrite.ID == guildRoles[i].ID {
						if overwrite.Allow & discordgo.PermissionAdministrator != 0 {
							perms.Administrator = true
						}
						if overwrite.Allow & discordgo.PermissionKickMembers != 0 {
							perms.Kick = true
						}
						if overwrite.Allow & discordgo.PermissionBanMembers != 0 {
							perms.Ban = true
						}
						if overwrite.Deny & discordgo.PermissionAdministrator != 0 {
							perms.Administrator = false
						}
						if overwrite.Deny & discordgo.PermissionKickMembers != 0 {
							perms.Kick = false
						}
						if overwrite.Deny & discordgo.PermissionBanMembers != 0 {
							perms.Ban = false
						}
					}
				}
			}
		}
	}

	for _, overwrite := range channel.PermissionOverwrites {
		if overwrite.Type == discordgo.PermissionOverwriteTypeMember && overwrite.ID == userID {
			if overwrite.Allow & discordgo.PermissionAdministrator != 0 {
				perms.Administrator = true
			}
			if overwrite.Allow & discordgo.PermissionKickMembers != 0 {
				perms.Kick = true
			}
			if overwrite.Allow & discordgo.PermissionBanMembers != 0 {
				perms.Ban = true
			}
			if overwrite.Deny & discordgo.PermissionAdministrator != 0 {
				perms.Administrator = false
			}
			if overwrite.Deny & discordgo.PermissionKickMembers != 0 {
				perms.Kick = false
			}
			if overwrite.Deny & discordgo.PermissionBanMembers != 0 {
				perms.Ban = false
			}
		}
	}

	if server.OwnerID == userID {
		perms.Administrator = true
	}

	return perms, nil
}
func (discord *ClientDiscord) UserBan(user *services.User, reason string, rule int) (err error) {
	Log.Trace("Ban(", user.ServerID, ", ", user.UserID, ", ", reason, ", ", rule, ")")
	return discord.GuildBanCreateWithReason(user.ServerID, user.UserID, reason, 0)
}
func (discord *ClientDiscord) UserKick(user *services.User, reason string, rule int) (err error) {
	Log.Trace("Kick(", user.ServerID, ", ", user.UserID, ", ", reason, ", ", rule, ")")
	return discord.GuildMemberDeleteWithReason(user.ServerID, user.UserID, reason)
}

func (discord *ClientDiscord) GetServer(serverID string) (server *services.Server, err error) {
	guild, err := discord.State.Guild(serverID)
	if err != nil {
		return nil, err
	}

	voiceStates := make([]*services.VoiceState, len(guild.VoiceStates))
	for i := 0; i < len(voiceStates); i++ {
		vs := guild.VoiceStates[i]
		voiceStates[i] = &services.VoiceState{
			ChannelID: vs.ChannelID,
			UserID: vs.UserID,
			SessionID: vs.SessionID,
			Deaf: vs.Deaf,
			Mute: vs.Mute,
			SelfDeaf: vs.SelfDeaf,
			SelfMute: vs.SelfMute,
		}
	}

	return &services.Server{
		ServerID: serverID,
		Name: guild.Name,
		Region: guild.Region,
		OwnerID: guild.OwnerID,
		DefaultChannel: guild.SystemChannelID,
		VoiceStates: voiceStates,
	}, nil
}

func (discord *ClientDiscord) VoiceJoin(serverID, channelID string, muted, deafened bool) (err error) {
	for _, vc := range discord.VCs {
		if vc.GuildID == serverID {
			return vc.ChangeChannel(channelID, muted, deafened)
		}
	}

	vc, err := discord.ChannelVoiceJoin(serverID, channelID, muted, deafened)
	if err != nil {
		return err
	}

	discord.VCs = append(discord.VCs, vc)
	return nil
}
func (discord *ClientDiscord) VoiceLeave(serverID string) (err error) {
	for i := 0; i < len(discord.VCs); i++ {
		if discord.VCs[i].GuildID == serverID {
			if err := discord.VCs[i].Disconnect(); err != nil {
				return err
			}
			discord.VCs = append(discord.VCs[:i], discord.VCs[i+1:]...)
			return nil
		}
	}

	return services.Error("discord: VoiceLeave: unknown VC for server %s", serverID)
}
