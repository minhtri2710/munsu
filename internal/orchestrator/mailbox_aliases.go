package orchestrator

import "github.com/minhtri2710/munsu/internal/home"

const captainMarkerName = home.CaptainProvenanceMarkerName

type Envelope = home.Envelope
type ProcessingAck = home.ProcessingAck
type NotificationRef = home.NotificationRef
type Store = home.Store
type Receiver = home.Receiver
type Rank = home.Rank
type BoundSender = home.BoundSender
type BoundSendResult = home.BoundSendResult
type NotifyResult = home.NotifyResult

const (
	SchemaVersion       = home.SchemaVersion
	AckSchemaVersion    = home.AckSchemaVersion
	RankGeneral         = home.RankGeneral
	RankCaptain         = home.RankCaptain
	RankSoldier         = home.RankSoldier
	OutcomeAccepted     = home.OutcomeAccepted
	OutcomeBlocked      = home.OutcomeBlocked
	OutcomeDone         = home.OutcomeDone
	OutcomeFailed       = home.OutcomeFailed
	OutcomeNeedsDecisio = home.OutcomeNeedsDecisio
	OutcomePaused       = home.OutcomePaused
	InboxDir            = home.InboxDir
	OutboxDir           = home.OutboxDir
)

var NewStore = home.NewStore
var NewReceiver = home.NewReceiver
var NewSoldierReceiver = home.NewSoldierReceiver
var ParseNotificationRef = home.ParseNotificationRef
var ReadHomeIdentity = home.ReadHomeIdentity
var WriteHomeIdentity = home.WriteHomeIdentity
var PayloadHashHex = home.PayloadHashHex
var ValidateEnvelope = home.ValidateEnvelope
var ValidateAck = home.ValidateAck
var ValidateProcessingAck = home.ValidateProcessingAck
var ValidatePathComponent = home.ValidatePathComponent
var NewMessageID = home.NewMessageID
var ValidRank = home.ValidRank
var ValidOutcome = home.ValidOutcome
var GetInboxEnvelope = home.GetInboxEnvelope
var SaveSenderPending = home.SaveSenderPending
var RemoveSenderPending = home.RemoveSenderPending
var ListSenderPending = home.ListSenderPending
var NewEnvelope = home.NewEnvelope
var ListPendingInbox = home.ListPendingInbox
var IsAcked = home.IsAcked
var NotifyReceiverWithSender = home.NotifyReceiverWithSender
