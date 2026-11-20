package ingress

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"

	"github.com/aws/aws-lambda-go/events"
	"github.com/disgoorg/disgo/discord"

	"discord-ai-assistant/internal/command"
	"discord-ai-assistant/internal/help"
)

const (
	signatureHeader = "x-signature-ed25519"
	timestampHeader = "x-signature-timestamp"

	interactionTypePing    = 1
	interactionTypeCommand = 2

	contentTypeHeader = "Content-Type"
	contentTypeJSON   = "application/json"

	invalidSignatureBody   = "invalid request signature"
	invalidRequestBody     = "invalid request body"
	unsupportedTypeBody    = "unsupported interaction type"
	pongBody               = `{"type":1}`
	deferredBody           = `{"type":5}`
	privateDeferredBody    = `{"type":5,"data":{"flags":64}}`
	logWorkerInvokeFailed  = "worker invoke failed"
	responseTypeChannelMsg = 4
)

// 受け付けた依頼を処理できなかったときのログ。
const (
	logBodyDecodeFailed        = "request body decode failed"
	logSignatureInvalid        = "signature verification failed"
	logInteractionDecodeFailed = "interaction decode failed"
	logUnsupportedType         = "unsupported interaction type"
	logKeyReason               = "reason"
	logKeyError                = "error"
	logKeyType                 = "type"
)

// interaction は Discord の interaction のうち、振り分けに使う最小の部分。
type interaction struct {
	Type           int                 `json:"type"`
	ID             string              `json:"id"`
	ApplicationID  string              `json:"application_id"`
	Token          string              `json:"token"`
	GuildID        string              `json:"guild_id"`
	ChannelID      string              `json:"channel_id"`
	AppPermissions discord.Permissions `json:"app_permissions"` // 実行元チャンネルでの bot の権限
	Channel        struct {
		Type discord.ChannelType `json:"type"`
	} `json:"channel"`
	Member struct {
		Permissions discord.Permissions `json:"permissions"` // 実行元チャンネルでの実行者の権限
		User        struct {
			ID string `json:"id"`
		} `json:"user"`
	} `json:"member"`
	Data struct {
		Name    string `json:"name"`
		Options []struct {
			Name  string                               `json:"name"`
			Type  discord.ApplicationCommandOptionType `json:"type"`
			Value json.RawMessage                      `json:"value"`
		} `json:"options"`
		Resolved struct {
			Attachments map[string]command.Attachment `json:"attachments"`
		} `json:"resolved"`
	} `json:"data"`
}

type messageResponse struct {
	Type int `json:"type"`
	Data struct {
		Content string               `json:"content"`
		Flags   discord.MessageFlags `json:"flags,omitempty"`
	} `json:"data"`
}

// SecretInputs は、入力に秘密を含むコマンドかを答える。*command.Registry が満たす。
type SecretInputs interface {
	HasSecretInput(commandName string) bool
}

// Handler は Discord の interaction を受け、署名検証と振り分けを行う。
type Handler struct {
	publicKey ed25519.PublicKey
	invoker   WorkerInvoker
	secrets   SecretInputs
	logger    *slog.Logger
}

func NewHandler(publicKey ed25519.PublicKey, invoker WorkerInvoker, secrets SecretInputs, logger *slog.Logger) *Handler {
	return &Handler{publicKey: publicKey, invoker: invoker, secrets: secrets, logger: logger}
}

// Handle は署名を検証したうえで、interaction の種類に応じた応答を返す。
func (h *Handler) Handle(ctx context.Context, req events.LambdaFunctionURLRequest) (events.LambdaFunctionURLResponse, error) {
	body, err := requestBody(req)
	if err != nil {
		h.logger.Warn(logBodyDecodeFailed, logKeyError, err.Error())
		return textResponse(http.StatusUnauthorized, invalidSignatureBody), nil
	}
	signature := headerValue(req.Headers, signatureHeader)
	timestamp := headerValue(req.Headers, timestampHeader)
	if err := VerifySignature(h.publicKey, signature, timestamp, body); err != nil {
		h.logger.Warn(logSignatureInvalid, logKeyReason, err.Error())
		return textResponse(http.StatusUnauthorized, invalidSignatureBody), nil
	}

	var in interaction
	if err := json.Unmarshal(body, &in); err != nil {
		h.logger.Warn(logInteractionDecodeFailed, logKeyError, err.Error())
		return textResponse(http.StatusBadRequest, invalidRequestBody), nil
	}
	switch in.Type {
	case interactionTypePing:
		return jsonResponse(pongBody), nil
	case interactionTypeCommand:
		return h.handleCommand(ctx, in), nil
	default:
		h.logger.Warn(logUnsupportedType, logKeyType, in.Type)
		return textResponse(http.StatusBadRequest, unsupportedTypeBody), nil
	}
}

func (h *Handler) handleCommand(ctx context.Context, in interaction) events.LambdaFunctionURLResponse {
	if in.Data.Name == help.CommandName {
		return jsonResponse(ephemeralBody(help.Usage))
	}
	inv := toInvocation(in)
	// 入力に秘密を含むコマンドは、visibility の指定によらず実行者だけに応答する（入力をほかの参加者に見せない）。
	private := command.IsPrivate(inv.Options) || h.secrets.HasSecretInput(inv.CommandName)
	if err := h.invoker.InvokeAsync(ctx, inv); err != nil {
		h.logger.Error(logWorkerInvokeFailed, "command", inv.CommandName, "error", err)
		return jsonResponse(failureBody(private))
	}
	if private {
		return jsonResponse(privateDeferredBody)
	}
	return jsonResponse(deferredBody)
}

func toInvocation(in interaction) command.Invocation {
	options := make(map[string]string, len(in.Data.Options))
	var attachments map[string]command.Attachment
	for _, o := range in.Data.Options {
		if o.Type != discord.ApplicationCommandOptionTypeAttachment {
			options[o.Name] = optionValue(o.Value)
			continue
		}
		if attachments == nil {
			attachments = make(map[string]command.Attachment)
		}
		// 添付ファイル型の value は添付の ID で、実体は data.resolved.attachments にある。
		attachments[o.Name] = in.Data.Resolved.Attachments[optionValue(o.Value)]
	}
	return command.Invocation{
		ApplicationID:     in.ApplicationID,
		InteractionToken:  in.Token,
		ChannelID:         in.ChannelID,
		CommandName:       in.Data.Name,
		Options:           options,
		Attachments:       attachments,
		AppPermissions:    in.AppPermissions,
		MemberPermissions: in.Member.Permissions,
		InteractionID:     in.ID,
		GuildID:           in.GuildID,
		UserID:            in.Member.User.ID,
		ChannelType:       in.Channel.Type,
	}
}

// optionValue は文字列をそのまま、ほかの型を JSON の表記で返す。
func optionValue(raw json.RawMessage) string {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	return string(raw)
}

func failureBody(private bool) string {
	var msg messageResponse
	msg.Type = responseTypeChannelMsg
	msg.Data.Content = command.FailureMessage
	if private {
		msg.Data.Flags = discord.MessageFlagEphemeral
	}
	b, _ := json.Marshal(msg)
	return string(b)
}

func ephemeralBody(content string) string {
	var msg messageResponse
	msg.Type = responseTypeChannelMsg
	msg.Data.Content = content
	msg.Data.Flags = discord.MessageFlagEphemeral
	b, _ := json.Marshal(msg)
	return string(b)
}

func requestBody(req events.LambdaFunctionURLRequest) ([]byte, error) {
	if !req.IsBase64Encoded {
		return []byte(req.Body), nil
	}
	return base64.StdEncoding.DecodeString(req.Body)
}

// headerValue は大文字小文字を区別せずにヘッダの値を探す。
func headerValue(headers map[string]string, name string) string {
	for k, v := range headers {
		if strings.EqualFold(k, name) {
			return v
		}
	}
	return ""
}

func jsonResponse(body string) events.LambdaFunctionURLResponse {
	return events.LambdaFunctionURLResponse{
		StatusCode: http.StatusOK,
		Headers:    map[string]string{contentTypeHeader: contentTypeJSON},
		Body:       body,
	}
}

func textResponse(status int, body string) events.LambdaFunctionURLResponse {
	return events.LambdaFunctionURLResponse{StatusCode: status, Body: body}
}
