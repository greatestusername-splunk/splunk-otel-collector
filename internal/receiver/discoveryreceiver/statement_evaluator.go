// Copyright Splunk, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package discoveryreceiver

import (
	"fmt"
	"sync"
	"time"

	"github.com/antonmedv/expr"
	"github.com/open-telemetry/opentelemetry-collector-contrib/extension/observer"
	"go.opentelemetry.io/collector/config"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/signalfx/splunk-otel-collector/internal/receiver/discoveryreceiver/statussources"
)

var _ zapcore.Core = (*statementEvaluator)(nil)

const (
	statementMatch = "statement.match"
)

// statementEvaluator conforms to a zapcore.Core to intercept component log statements and
// determine if they match any configured Status match rules. If so, they emit log records
// for the matching statement.
type statementEvaluator struct {
	*evaluator
	// this is the logger to share with other components to evaluate their statements and produce plog.Logs
	evaluatedLogger *zap.Logger
	encoder         zapcore.Encoder
}

func newStatementEvaluator(logger *zap.Logger, config *Config, pLogs chan plog.Logs, correlations *correlationStore) (*statementEvaluator, error) {
	zapConfig := zap.NewProductionConfig()
	zapConfig.Level = zap.NewAtomicLevelAt(zap.DebugLevel)
	zapConfig.Sampling.Initial = 1
	zapConfig.Sampling.Thereafter = 1
	encoder := zapcore.NewJSONEncoder(zap.NewProductionEncoderConfig())

	le := &statementEvaluator{
		evaluator: &evaluator{
			logger:        logger,
			config:        config,
			correlations:  correlations,
			pLogs:         pLogs,
			alreadyLogged: &sync.Map{},
			// TODO: provide more capable env w/ resource and log record attributes
			exprEnv: func(pattern string) expr.Option {
				return expr.Env(map[string]string{
					"body": pattern,
				})
			},
		},
		encoder: encoder,
	}
	var err error
	if le.evaluatedLogger, err = zapConfig.Build(
		// zap.OnFatal must not be WriteThenFatal or WriteThenNoop since it's rewritten to be WriteThenFatal
		// https://github.com/uber-go/zap/blob/e06e09a6d396031c89b87383eef3cad6f647cf2c/logger.go#L315.
		// Using an arbitrary action offset.
		zap.OnFatal(zapcore.WriteThenFatal+100),
		zap.WrapCore(func(core zapcore.Core) zapcore.Core { return le }),
	); err != nil {
		return nil, err
	}
	return le, nil
}

// Enabled is a zapcore.Core method. We should be enabled for all
// levels since we want to intercept all statements.
func (se *statementEvaluator) Enabled(lvl zapcore.Level) bool {
	return true
}

// With is a zapcore.Core method. We clone ourselves so all
// modified downstream loggers are still evaluated.
func (se *statementEvaluator) With(fields []zapcore.Field) zapcore.Core {
	cp := *se
	cp.encoder = se.encoder.Clone()
	for i := range fields {
		fields[i].AddTo(cp.encoder)
	}
	return &cp
}

// Check is a zapcore.Core method. Similar to Enabled() we want to
// return a valid CheckedEntry for all logging attempts to intercept
// all statements.
func (se *statementEvaluator) Check(entry zapcore.Entry, checkedEntry *zapcore.CheckedEntry) *zapcore.CheckedEntry {
	return checkedEntry.AddCore(entry, se)
}

// Write is a zapcore.Core method. This is where the logged entry
// is converted to a statussources.Statement, if from a downstream receiver,
// and its content is evaluated for status matches and plog.Logs translation/submission.
func (se *statementEvaluator) Write(entry zapcore.Entry, fields []zapcore.Field) error {
	statement, err := statussources.StatementFromZapCoreEntry(se.encoder, entry, fields)
	if err != nil {
		return err
	}
	if name, ok := statement.Fields["name"]; ok {
		if cid, err := config.NewComponentIDFromString(fmt.Sprintf("%v", name)); err == nil {
			if cid.Type() == "receiver_creator" && cid.Name() == se.config.ID().String() {
				// this is from our internal Receiver Creator and not a generated receiver, so write
				// it to our logger core without submitting the entry for evaluation
				if ce := se.logger.Check(entry.Level, ""); ce != nil {
					// forward to our logger now that we know entry.Level is accepted
					se.logger.Core().Write(entry, fields)
				}
				return nil
			}
		}
	}

	if pLogs := se.evaluateStatement(statement); pLogs.LogRecordCount() > 0 {
		se.pLogs <- pLogs
	}

	return nil
}

// Sync is a zapcore.Core method.
func (se *statementEvaluator) Sync() error {
	return nil
}

// evaluateStatement will convert the provided statussources.Statement into a plog.LogRecord
// and match it against the applicable configured ReceiverEntry's status Statement.[]Match
func (se *statementEvaluator) evaluateStatement(statement *statussources.Statement) plog.Logs {
	se.logger.Debug("evaluating statement", zap.Any("statement", statement))
	statementLogRecord := statement.ToLogRecord()

	pLogs := plog.NewLogs()
	receiverID, endpointID := idsFromStatementLogRecord(statementLogRecord)
	if receiverID == statussources.NoType || endpointID == "" {
		// statement evaluation requires both a populated receiver.ID and EndpointID
		se.logger.Debug("unable to evaluate statement from receiver", zap.String("receiver", receiverID.String()))
		return pLogs
	}

	rEntry, ok := se.config.Receivers[receiverID]
	if !ok {
		se.logger.Info("No matching configured receiver for statement status evaluation", zap.String("receiver", receiverID.String()))
		return pLogs
	}

	if rEntry.Status == nil {
		return pLogs
	}

	correlation := se.correlations.correlationForReceiver(receiverID, endpointID)

	stagePLogs := plog.NewLogs()
	rLog := stagePLogs.ResourceLogs().AppendEmpty()
	rAttrs := rLog.Resource().Attributes()
	fromAttrs := pcommon.NewMap()
	fromAttrs.InsertString(statussources.ReceiverTypeAttr, string(receiverID.Type()))
	fromAttrs.InsertString(statussources.ReceiverNameAttr, receiverID.Name())
	fromAttrs.InsertString(statussources.EndpointIDAttr, string(correlation.endpoint.ID))
	se.correlateResourceAttributes(fromAttrs, rAttrs, correlation)
	rAttrs.InsertString(eventTypeAttr, statementMatch)
	rAttrs.InsertString(receiverRuleAttr, rEntry.Rule)
	logRecords := rLog.ScopeLogs().AppendEmpty().LogRecords()

	var matchFound bool

	body := statementLogRecord.Body().AsString()
	for status, matches := range rEntry.Status.Statements {
		for _, match := range matches {
			if shouldLog, err := se.evaluateMatch(match, body, status, receiverID, endpointID); err != nil {
				se.logger.Info(fmt.Sprintf("Error evaluating %s statement match", status), zap.Error(err))
				continue
			} else if !shouldLog {
				continue
			}
			matchFound = true
			logRecord := logRecords.AppendEmpty()
			var desiredRecord LogRecord
			if match.Record != nil {
				desiredRecord = *match.Record
			}
			statementLogRecord.CopyTo(logRecord)
			if desiredRecord.Body != "" {
				logRecord.Body().SetStringVal(desiredRecord.Body)
			}
			if len(desiredRecord.Attributes) > 0 {
				for k, v := range desiredRecord.Attributes {
					logRecord.Attributes().InsertString(k, v)
				}
			}
			severityText := desiredRecord.SeverityText
			if severityText == "" {
				severityText = logRecord.SeverityText()
				if severityText == "" {
					severityText = "info"
				}
			}
			logRecord.SetSeverityText(severityText)
			logRecord.Attributes().UpsertString(statusAttr, status)
			logRecord.SetTimestamp(pcommon.NewTimestampFromTime(statement.Time))
			logRecord.SetObservedTimestamp(pcommon.NewTimestampFromTime(time.Now()))
		}
	}

	if matchFound {
		pLogs = stagePLogs
	}
	return pLogs
}

func idsFromStatementLogRecord(record plog.LogRecord) (config.ComponentID, observer.EndpointID) {
	// The receiver creator sets dynamically created receiver names as the zap "name" field for their component logger.
	nameAttr, ok := record.Attributes().Get("name")
	if !ok {
		// there is nothing we can do without a receiver name
		return statussources.NoType, ""
	}
	receiverName := nameAttr.AsString()

	// The receiver creator will log an initial start statement not from the underlying receiver's logger.
	// These statements have an "endpoint_id" field and the "name" field won't include the necessary "receiver_creator/"
	// and "{endpoint=<endpoint.Target>}" separators. In this case we get the EndpointID, if any, and form a placeholder name of desired form.
	if endpointIDAttr, ok := record.Attributes().Get("endpoint_id"); ok {
		receiverName = fmt.Sprintf(`%s/receiver_creator/<PLACEHOLDER>/{endpoint="PLACEHOLDER"}/%s`, receiverName, endpointIDAttr.AsString())
	}

	return statussources.ReceiverNameToIDs(receiverName)
}
