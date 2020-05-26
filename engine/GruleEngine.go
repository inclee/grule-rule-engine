package engine

import (
	"sort"
	"time"

	"github.com/hyperjumptech/grule-rule-engine/ast"
	"github.com/hyperjumptech/grule-rule-engine/events"
	"github.com/hyperjumptech/grule-rule-engine/pkg/eventbus"
	"github.com/juju/errors"
	"github.com/sirupsen/logrus"
)

var (
	// Logger is a logrus instance with default fields for grule
	log = logrus.WithFields(logrus.Fields{
		"lib":    "grule",
		"struct": "GruleEngineV2",
	})
)

// NewGruleEngine will create new instance of GruleEngine struct.
// It will set the max cycle to 5000
func NewGruleEngine() *GruleEngine {
	return &GruleEngine{
		MaxCycle: 5000,
	}
}

// GruleEngine is the engine structure. It has the Execute method to start the engine to work.
type GruleEngine struct {
	MaxCycle uint64
}

// Execute function will execute a knowledge evaluation and action against data context.
// The engine also do conflict resolution of which rule to execute.
func (g *GruleEngine) Execute(dataCtx *ast.DataContext, knowledge *ast.KnowledgeBase, memory *ast.WorkingMemory) error {
	RuleEnginePublisher := eventbus.DefaultBrooker.GetPublisher(events.RuleEngineEventTopic)
	RuleEntryPublisher := eventbus.DefaultBrooker.GetPublisher(events.RuleEntryEventTopic)

	// emit engine start event
	RuleEnginePublisher.Publish(&events.RuleEngineEvent{
		EventType: events.RuleEngineStartEvent,
		Cycle:     0,
	})

	log.Debugf("Starting rule execution using knowledge '%s' version %s. Contains %d rule entries", knowledge.Name, knowledge.Version, len(knowledge.RuleEntries))

	knowledge.WorkingMemory = memory

	// Prepare the timer, we need to measure the processing time in debug mode.
	startTime := time.Now()

	// Prepare the build-in function and add to datacontext.
	defunc := &ast.BuildInFunctions{
		Knowledge:     knowledge,
		WorkingMemory: memory,
	}
	dataCtx.Add("DEFUNC", defunc)

	// Working memory need to be resetted. all Expression will be set as not evaluated.
	log.Debugf("Resetting Working memory")
	knowledge.WorkingMemory.ResetAll()

	// Initialize all AST with datacontext and working memory
	log.Debugf("Initializing Context")
	knowledge.InitializeContext(dataCtx, memory)

	var cycle uint64

	/*
		Un-limitted loop as long as there are rule to execute.
		We need to add safety mechanism to detect unlimitted loop as there are posibility executed rule are not changing
		data context which makes rules to get executed again and again.
	*/
	for {

		// add the cycle counter
		cycle++

		// emit engine cycle event
		RuleEnginePublisher.Publish(&events.RuleEngineEvent{
			EventType: events.RuleEngineCycleEvent,
			Cycle:     cycle,
		})

		log.Debugf("Cycle #%d", cycle)
		// if cycle is above the maximum allowed cycle, returnan error indicated the cycle has ended.
		if cycle > g.MaxCycle {

			// create the error
			err := errors.Errorf("GruleEngine successfully selected rule candidate for execution after %d cycles, this could possibly caused by rule entry(s) that keep added into execution pool but when executed it does not change any data in context. Please evaluate your rule entries \"When\" and \"Then\" scope. You can adjust the maximum cycle using GruleEngine.MaxCycle variable.", g.MaxCycle)

			// emit engine error event
			RuleEnginePublisher.Publish(&events.RuleEngineEvent{
				EventType: events.RuleEngineErrorEvent,
				Cycle:     cycle,
				Error:     err,
			})

			return err
		}
		// Select all rule entry that can be executed.
		log.Tracef("Select all rule entry that can be executed.")
		ruleEntries := make([]*ast.RuleEntry, 0)
		for _, v := range knowledge.RuleEntries {
			ruleEntries = append(ruleEntries, v)
		}
		sort.SliceStable(ruleEntries, func(i, j int) bool {
			return ruleEntries[i].Salience > ruleEntries[j].Salience
		})
		for _, v := range ruleEntries {
			// test if this rule entry v can execute.
			can, err := v.Evaluate()
			if err != nil {
				log.Errorf("Failed testing condition for rule : %s. Got error %v", v.Name, err)
				// No longer return error, since unavailability of variable or fact in context might be intentional.
			}
			if can {
				RuleEntryPublisher.Publish(&events.RuleEntryEvent{
					EventType: events.RuleEntryExecuteStartEvent,
					RuleName:  v.Name,
				})
				err = v.Execute()
				if err != nil {
					log.Errorf("Failed execution rule : %s. Got error %v", v.Name, err)
				}
				RuleEntryPublisher.Publish(&events.RuleEntryEvent{
					EventType: events.RuleEntryExecuteEndEvent,
					RuleName:  v.Name,
				})
			}
		}

	}
	log.Debugf("Finished Rules execution. With knowledge base '%s' version %s. Total #%d cycles. Duration %d ms.", knowledge.Name, knowledge.Version, cycle, time.Now().Sub(startTime).Milliseconds())

	// emit engine finish event
	RuleEnginePublisher.Publish(&events.RuleEngineEvent{
		EventType: events.RuleEngineEndEvent,
		Cycle:     cycle,
	})

	return nil
}
