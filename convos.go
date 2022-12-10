package discord

import (
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/Clinet/clinet_cmds"
	"github.com/Clinet/clinet_convos"
)

func convoHandler(session *discordgo.Session, message *discordgo.Message, channel *discordgo.Channel) (cmdResps []*cmds.CmdResp, err error) {
	if message == nil {
		return nil, cmds.ErrCmdEmptyMsg
	}
	if message.Author.Bot {
		return nil, nil
	}
	content := message.Content
	if content == "" {
		return nil, nil
	}

	//Determine interaction type and how it was called
	prefix := ""
	convo := false
	if strings.Contains(content, "<@" + Discord.User.ID + ">") {
		prefix = "<@" + Discord.User.ID + ">"
		convo = true
	} else if strings.Contains(content, "<@!" + Discord.User.ID + ">") {
		prefix = "<@!" + Discord.User.ID + ">"
		convo = true
	}

	if !convo {
		return nil, nil
	}
	content = strings.ReplaceAll(content, " " + prefix + " ", " ")
	content = strings.ReplaceAll(content, " " + prefix, "")
	content = strings.ReplaceAll(content, prefix + " ", "")
	content = strings.ReplaceAll(content, prefix, "")

	//Start typing!
	stopTyping := false
	go func(shouldStop *bool, channelID string) {
		for {
			if shouldStop == nil || *shouldStop {
				break
			}
			err := session.ChannelTyping(channelID)
			if err != nil {
				break
			}
			time.Sleep(time.Second * 2) //2 seconds is about long enough?
		}
	}(&stopTyping, message.ChannelID)

	cmdResps = make([]*cmds.CmdResp, 0)
	conversation := convos.NewConversation()
	if oldConversation, err := Discord.Storage.ServerGet(channel.GuildID, "conversations_" + message.Author.ID); err == nil {
		switch oldConversation.(type) {
			case convos.Conversation:
				conversation = oldConversation.(convos.Conversation)
			default:
				Log.Trace("Skipping broken conversation record")
		}
	}
	conversationState := conversation.QueryText(content)
	if len(conversationState.Errors) > 0 {
		for _, csErr := range conversationState.Errors {
			Log.Error(csErr)
		}
	}
	if conversationState.Response != nil {
		//TODO: Dynamically build either an embed response or a simple conversation response
		cmdResps = append(cmdResps, cmds.NewCmdRespMsg(conversationState.Response.TextSimple))
		Discord.Storage.ServerSet(channel.GuildID, "conversations_" + message.Author.ID, conversation)
	} else {
		//TODO: Make a nice error message for failed queries in a conversation
		cmdResps = append(cmdResps, cmds.NewCmdRespMsg("Erm... well this is awkward. I don't have an answer for that."))
		Discord.Storage.ServerDel(channel.GuildID, "conversations_" + message.Author.ID)
	}

	stopTyping = true

	return cmdResps, nil
}
