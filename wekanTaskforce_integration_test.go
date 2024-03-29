//go:build integration
// +build integration

// nolint:errcheck

package main

import (
  "testing"

  "github.com/signaux-faibles/libwekan"
  "github.com/stretchr/testify/assert"
  "github.com/stretchr/testify/require"
)

func createBoard(t *testing.T, wekan libwekan.Wekan, suffix string) (libwekan.Board, libwekan.Swimlane, libwekan.List) {
  board := libwekan.BuildBoard(t.Name()+"_Title"+suffix, t.Name()+"_Slug"+suffix, "board")
  wekan.InsertBoard(ctx, board)
  swimlane := libwekan.BuildSwimlane(board.ID, "swimlane", t.Name()+"_Swimlane"+suffix, 0)
  wekan.InsertSwimlane(ctx, swimlane)
  list := libwekan.BuildList(board.ID, t.Name()+"_List"+suffix, 0)
  wekan.InsertList(ctx, list)
  return board, swimlane, list
}

func createUser(
    t *testing.T,
    wekan libwekan.Wekan,
    suffix string,
    board *libwekan.Board,
    card *libwekan.Card,
) libwekan.User {
  name := t.Name() + suffix
  user := libwekan.BuildUser(name, name, name)
  err := wekan.InsertUser(ctx, user)
  require.NoError(t, err)
  if board != nil {
    boardMember := libwekan.BoardMember{
      UserID:   user.ID,
      IsActive: true,
    }
    err = wekan.AddMemberToBoard(ctx, board.ID, boardMember)
    require.NoError(t, err)
  }
  if card != nil {
    err := wekan.AddSelfMemberToCard(ctx, *card, user)
    require.NoError(t, err)
  }
  return user
}

func createCard(
    t *testing.T,
    wekan libwekan.Wekan,
    suffix string,
    boardID libwekan.BoardID,
    swimlaneID libwekan.SwimlaneID,
    listID libwekan.ListID,
) libwekan.Card {
  name := t.Name() + suffix
  card := libwekan.BuildCard(boardID, listID, swimlaneID, name, "desc : "+name, wekan.AdminID())
  wekan.InsertCard(ctx, card)
  return card
}

func createLabel(t *testing.T, wekan libwekan.Wekan, suffix string, boardID libwekan.BoardID, cardID *libwekan.CardID) libwekan.BoardLabel {
  boardLabel := libwekan.NewBoardLabel(t.Name()+"_Label"+suffix, "red")
  board, _ := boardID.GetDocument(ctx, &wekan)
  wekan.InsertBoardLabel(ctx, board, boardLabel)
  if cardID != nil {
    card, _ := cardID.GetDocument(ctx, &wekan)
    wekan.AddLabelToCard(ctx, card.ID, boardLabel.ID)
  }
  return boardLabel
}

func TestWekanTaskforce_AddMissingRules_whenEverythingFine(t *testing.T) {
  // GIVEN
  wekan := restoreMongoDumpInDatabase(mongodb, "", t, "")
  ass := assert.New(t)

  board, swimlane, list := createBoard(t, wekan, "board")
  cardOnBoard := createCard(t, wekan, "card", board.ID, swimlane.ID, list.ID)
  userOnBoard := createUser(t, wekan, "user", &board, nil)
  label := createLabel(t, wekan, "label", board.ID, &cardOnBoard.ID)

  users := Users{
    Username(userOnBoard.Username): User{
      scope:      []string{"wekan"},
      email:      Username(userOnBoard.Username),
      boards:     []string{string(board.Slug)},
      taskforces: []string{string(label.Name)},
    },
  }

  // WHEN
  err := pipeline.StopAfter(wekan, users, stageAddMissingRulesAndCardMembership)
  printErrChain(err, 0)
  require.NoError(t, err)

  // THEN
  actualCard, _ := cardOnBoard.ID.GetDocument(ctx, &wekan)
  ass.Contains(actualCard.Members, userOnBoard.ID)
  rules, err := wekan.SelectRulesFromBoardID(ctx, board.ID)
  ass.NoError(err)
  require.Len(t, rules, 2)
  actualRule := rules[0]
  ass.Equal(string(userOnBoard.Username), string(actualRule.Action.Username))
  ass.Equal(label.ID, actualRule.Trigger.LabelID)
}

func TestWekanTaskforce_AddMissingRules_whenScopeNotWekan(t *testing.T) {
  // GIVEN
  wekan := restoreMongoDumpInDatabase(mongodb, "", t, "")
  ass := assert.New(t)

  board, swimlane, list := createBoard(t, wekan, "board")
  cardOnBoard := createCard(t, wekan, "card", board.ID, swimlane.ID, list.ID)
  userOnBoard := createUser(t, wekan, "user", &board, nil)
  label := createLabel(t, wekan, "label", board.ID, &cardOnBoard.ID)

  users := Users{
    Username(userOnBoard.Username): User{
      scope:      []string{""},
      email:      Username(userOnBoard.Username),
      boards:     []string{string(board.Slug)},
      taskforces: []string{string(label.Name)},
    },
  }

  // WHEN
  err := pipeline.StopAfter(wekan, users.selectScopeWekan(), stageAddMissingRulesAndCardMembership)
  require.NoError(t, err)

  // THEN
  actualCard, _ := cardOnBoard.ID.GetDocument(ctx, &wekan)
  ass.NotContains(actualCard.Members, userOnBoard.ID)
  rules, err := wekan.SelectRulesFromBoardID(ctx, board.ID)
  ass.Len(rules, 0)
}

func TestWekanTaskforce_AddMissingRules_WhenInactiveMember(t *testing.T) {
  // GIVEN
  wekan := restoreMongoDumpInDatabase(mongodb, "", t, "")
  ass := assert.New(t)

  board, swimlane, list := createBoard(t, wekan, "board")
  cardOnBoard := createCard(t, wekan, "card", board.ID, swimlane.ID, list.ID)
  userOnBoard := createUser(t, wekan, "user", &board, nil)
  label := createLabel(t, wekan, "label", board.ID, &cardOnBoard.ID)

  users := Users{
    Username(userOnBoard.Username): User{
      scope:      []string{"wekan"},
      email:      Username(userOnBoard.Username),
      boards:     []string{},
      taskforces: []string{string(label.Name)},
    },
  }

  // WHEN
  err := pipeline.StopAfter(wekan, users, stageAddMissingRulesAndCardMembership)
  ass.NoError(err)

  // THEN
  actualCard, _ := cardOnBoard.ID.GetDocument(ctx, &wekan)
  ass.NotContains(actualCard.Members, userOnBoard.ID)
  rules, err := wekan.SelectRulesFromBoardID(ctx, board.ID)
  ass.Len(rules, 0)
}

func TestWekanTaskforce_AddMissingRules_WhenNotMember(t *testing.T) {
  // GIVEN
  wekan := restoreMongoDumpInDatabase(mongodb, "", t, "")
  ass := assert.New(t)

  board, swimlane, list := createBoard(t, wekan, "board")
  cardOnBoard := createCard(t, wekan, "card", board.ID, swimlane.ID, list.ID)
  userOnBoard := createUser(t, wekan, "user", nil, nil)
  label := createLabel(t, wekan, "label", board.ID, &cardOnBoard.ID)

  users := Users{
    Username(userOnBoard.Username): User{
      scope:      []string{"wekan"},
      email:      Username(userOnBoard.Username),
      boards:     []string{},
      taskforces: []string{string(label.Name)},
    },
  }

  // WHEN
  err := pipeline.StopAfter(wekan, users, stageAddMissingRulesAndCardMembership)
  ass.NoError(err)

  // THEN
  actualCard, _ := cardOnBoard.ID.GetDocument(ctx, &wekan)
  ass.NotContains(actualCard.Members, userOnBoard.ID)
  rules, err := wekan.SelectRulesFromBoardID(ctx, board.ID)
  ass.Len(rules, 0)
}

func TestWekanTaskforce_AddMissingRules_whenBoardHasNotLabel(t *testing.T) {
  // GIVEN
  wekan := restoreMongoDumpInDatabase(mongodb, "", t, "")
  ass := assert.New(t)

  board, swimlane, list := createBoard(t, wekan, "board")
  cardOnBoard := createCard(t, wekan, "card", board.ID, swimlane.ID, list.ID)
  userOnBoard := createUser(t, wekan, "user", nil, nil)
  // label := createLabel(t, wekan, "label", board.ID, &cardOnBoard.ID)

  users := Users{
    Username(userOnBoard.Username): User{
      scope:      []string{"wekan"},
      email:      Username(userOnBoard.Username),
      boards:     []string{},
      taskforces: []string{"fakeLabel"},
    },
  }

  // WHEN
  err := pipeline.StopAfter(wekan, users, stageAddMissingRulesAndCardMembership)
  ass.NoError(err)

  // THEN
  actualCard, _ := cardOnBoard.ID.GetDocument(ctx, &wekan)
  ass.NotContains(actualCard.Members, userOnBoard.ID)
  rules, err := wekan.SelectRulesFromBoardID(ctx, board.ID)
  ass.Len(rules, 0)
}

func TestWekanTaskforce_RemoveExtraRules_whenUserLosesTaskforce(t *testing.T) {
  // GIVEN
  wekan := restoreMongoDumpInDatabase(mongodb, "", t, "")
  ass := assert.New(t)

  board, swimlane, list := createBoard(t, wekan, "board")
  cardOnBoard := createCard(t, wekan, "card", board.ID, swimlane.ID, list.ID)
  userOnBoard := createUser(t, wekan, "user", &board, nil)
  label := createLabel(t, wekan, "label", board.ID, &cardOnBoard.ID)

  initialUsers := Users{
    Username(userOnBoard.Username): User{
      scope:      []string{"wekan"},
      email:      Username(userOnBoard.Username),
      boards:     []string{string(board.Slug)},
      taskforces: []string{string(label.Name)},
    },
  }

  err := pipeline.StopAfter(wekan, initialUsers, stageAddMissingRulesAndCardMembership)
  printErrChain(err, 0)
  require.NoError(t, err)

  users := Users{
    Username(userOnBoard.Username): User{
      scope:      []string{"wekan"},
      email:      Username(userOnBoard.Username),
      boards:     []string{string(board.Slug)},
      taskforces: []string{},
    },
  }

  // WHEN
  err = pipeline.StopAfter(wekan, users, stageRemoveExtraRulesAndCardMembership)
  printErrChain(err, 0)
  require.NoError(t, err)

  // THEN
  actualCard, _ := cardOnBoard.ID.GetDocument(ctx, &wekan)
  ass.NotContains(actualCard.Members, userOnBoard.ID)
  rules, err := wekan.SelectRulesFromBoardID(ctx, board.ID)
  ass.Len(rules, 0)
}

func TestWekanTaskforce_RemoveExtraRules_whenUserLosesBoard(t *testing.T) {
  // GIVEN
  wekan := restoreMongoDumpInDatabase(mongodb, "", t, "TestWekanTaskforce_RemoveExtraRules_whenUserLosesBoard_Slugboard")
  ass := assert.New(t)

  board, swimlane, list := createBoard(t, wekan, "board")
  cardOnBoard := createCard(t, wekan, "card", board.ID, swimlane.ID, list.ID)
  userOnBoard := createUser(t, wekan, "user", &board, nil)
  label := createLabel(t, wekan, "label", board.ID, &cardOnBoard.ID)

  initialUsers := Users{
    Username(userOnBoard.Username): User{
      scope:      []string{"wekan"},
      email:      Username(userOnBoard.Username),
      boards:     []string{string(board.Slug)},
      taskforces: []string{string(label.Name)},
    },
  }

  err := pipeline.StopAfter(wekan, initialUsers, stageAddMissingRulesAndCardMembership)
  printErrChain(err, 0)
  require.NoError(t, err)

  users := Users{
    Username(userOnBoard.Username): User{
      scope:      []string{"wekan"},
      email:      Username(userOnBoard.Username),
      boards:     []string{},
      taskforces: []string{string(label.Name)},
    },
  }

  // WHEN
  err = pipeline.StopAfter(wekan, users, stageRemoveExtraRulesAndCardMembership)
  printErrChain(err, 0)
  require.NoError(t, err)

  // THEN
  actualCard, _ := cardOnBoard.ID.GetDocument(ctx, &wekan)
  ass.NotContains(actualCard.Members, userOnBoard.ID)
  rules, err := wekan.SelectRulesFromBoardID(ctx, board.ID)
  ass.Len(rules, 0)
}

func TestWekanTaskforce_RemoveExtraRules_whenUserLosesWekanScope(t *testing.T) {
  // GIVEN
  wekan := restoreMongoDumpInDatabase(mongodb, "", t, "")
  ass := assert.New(t)

  board, swimlane, list := createBoard(t, wekan, "board")
  cardOnBoard := createCard(t, wekan, "card", board.ID, swimlane.ID, list.ID)
  userOnBoard := createUser(t, wekan, "user", &board, nil)
  label := createLabel(t, wekan, "label", board.ID, &cardOnBoard.ID)

  initialUsers := Users{
    Username(userOnBoard.Username): User{
      scope:      []string{"wekan"},
      email:      Username(userOnBoard.Username),
      boards:     []string{string(board.Slug)},
      taskforces: []string{string(label.Name)},
    },
  }

  err := pipeline.StopAfter(wekan, initialUsers, stageAddMissingRulesAndCardMembership)
  printErrChain(err, 0)
  require.NoError(t, err)

  users := Users{
    Username(userOnBoard.Username): User{
      scope:      []string{},
      email:      Username(userOnBoard.Username),
      boards:     []string{string(board.Slug)},
      taskforces: []string{string(label.Name)},
    },
  }

  // WHEN
  err = pipeline.StopAfter(wekan, users.selectScopeWekan(), stageRemoveExtraRulesAndCardMembership)
  printErrChain(err, 0)
  require.NoError(t, err)

  // THEN
  actualCard, _ := cardOnBoard.ID.GetDocument(ctx, &wekan)
  ass.NotContains(actualCard.Members, userOnBoard.ID)
  rules, err := wekan.SelectRulesFromBoardID(ctx, board.ID)
  ass.Len(rules, 0)
}
