package main

import "github.com/jackielii/live-partials/examples/labdata"

// The lab's data and simulation are shared with the other stages.
type (
	store         = labdata.Store
	series        = labdata.Series
	seriesSummary = labdata.SeriesSummary
	person        = labdata.Person
	draftRow      = labdata.DraftRow
	job           = labdata.Job
)
