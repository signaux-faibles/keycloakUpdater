package main

import (
	"context"

	"github.com/signaux-faibles/libwekan"

	"keycloakUpdater/v2/pkg/logger"
)

type BoardsMembers map[libwekan.BoardSlug]Users

func manageBoardsMembers(wekan libwekan.Wekan, fromConfig Users) error {
	logContext := logger.ContextForMethod(manageBoardsMembers)
	// périmètre du stage
	wekanBoardsMembers := fromConfig.inferBoardsMember()
	domainBoards, err := wekan.SelectDomainBoards(context.Background())
	if err != nil {
		return err
	}
	wekanBoardsMembers.addBoards(domainBoards)

	logger.Info("> inscrit les utilisateurs dans les tableaux", logContext)
	for boardSlug, boardMembers := range wekanBoardsMembers {
		err := updateBoardMembers(wekan, boardSlug, boardMembers)
		if err != nil {
			return err
		}
	}
	return nil
}

func updateBoardMembers(wekan libwekan.Wekan, boardSlug libwekan.BoardSlug, boardMembers Users) error {
	logContext := logger.ContextForMethod(updateBoardMembers).AddAny("board", boardSlug)
	board, err := wekan.GetBoardFromSlug(context.Background(), boardSlug)
	if err != nil {
		return err
	}

	currentUsersMap, currentUsersIDs, err := fetchCurrentWekanBoardMembers(wekan, board)
	if err != nil {
		return err
	}

	expectedUsersMap, expectedUsersIDs, err := fetchExpectedWekanBoardMembers(wekan, boardMembers)
	if err != nil {
		return err
	}

	alreadyBoardMembers, expectedInactiveBoardMembers, newBoardMembers := intersect(currentUsersIDs, expectedUsersIDs)

	logger.Debug(">> examine les nouvelles inscriptions", logContext)
	for _, userID := range append(alreadyBoardMembers, newBoardMembers...) {
		if err := ensureUserIsActiveBoardMember(wekan, expectedUsersMap[userID], board); err != nil {
			return err
		}
	}

	logger.Debug(">> examine les radiations", logContext)
	for _, userID := range expectedInactiveBoardMembers {
		if _, ok := currentUsersMap[userID]; ok {
			if err := ensureUserIsInactiveBoardMember(wekan, currentUsersMap[userID], board); err != nil {
				return err
			}
		}
	}

	// globalWekan.AdminUser() est administrateur de toutes les boards, appliquons la règle
	logger.Debug(">> vérifie la participation de l'admin", logContext)
	modified, err := wekan.EnsureUserIsBoardAdmin(context.Background(), board.ID, wekan.AdminID())
	if modified {
		logContext.AddAny("username", wekan.AdminUsername())
		logger.Notice(">>> donne les privilèges à l'admin", logContext)
	}
	return err
}

func ensureUserIsActiveBoardMember(wekan libwekan.Wekan, user libwekan.User, board libwekan.Board) error {
	logContext := logger.ContextForMethod(ensureUserIsActiveBoardMember).
		AddAny("username", user.Username).
		AddAny("board", board.Slug)
	logger.Debug(">>> examine l'utilisateur", logContext)
	modified, err := wekan.EnsureUserIsActiveBoardMember(context.Background(), board.ID, user.ID)
	if err != nil {
		return err
	}
	if modified {
		logger.Notice(">>> inscrit l'utilisateur sur le board", logContext)
	}
	return nil
}

func ensureUserIsInactiveBoardMember(wekan libwekan.Wekan, user libwekan.User, board libwekan.Board) error {
	logContext := logger.ContextForMethod(ensureUserIsInactiveBoardMember).
		AddAny("username", user.Username).
		AddAny("board", board.Slug)
	logger.Debug(">>> vérifie la non-participation", logContext)
	modified, err := wekan.EnsureUserIsInactiveBoardMember(context.Background(), board.ID, user.ID)
	if err != nil {
		return err
	}
	if modified {
		logger.Notice(">>> désinscrit l'utilisateur du board", logContext)
	}
	return nil
}

// liste les usernames présents sur la board, actifs ou non et le place dans currentMembers
func fetchCurrentWekanBoardMembers(wekan libwekan.Wekan, board libwekan.Board) (map[libwekan.UserID]libwekan.User, []libwekan.UserID, error) {
	currentMembersIDs := mapSlice(board.Members, func(member libwekan.BoardMember) libwekan.UserID { return member.UserID })
	currentMembers, err := wekan.GetUsersFromIDs(context.Background(), currentMembersIDs)
	if err != nil {
		return nil, nil, err
	}
	currentUserMap := mapifySlice(currentMembers, libwekan.User.GetID)
	currentGenuineUserMap := selectMapByValue(currentUserMap, selectGenuineUserFunc(wekan))
	currentGenuineUserIDs := keys(currentUserMap)
	return currentGenuineUserMap, currentGenuineUserIDs, nil
}

func fetchExpectedWekanBoardMembers(wekan libwekan.Wekan, boardMembers Users) (map[libwekan.UserID]libwekan.User, []libwekan.UserID, error) {
	// liste les usernames que l'on veut garder ou rendre actifs sur la board
	wantedMembersUsernames := []libwekan.Username{}
	// globalWekan.AdminUser() est membre de toutes les boards, ajoutons le ici pour ne pas risquer de l'oublier dans les utilisateurs
	wantedMembersUsernames = append(wantedMembersUsernames, wekan.AdminUsername())
	for username := range boardMembers {
		wantedMembersUsernames = append(wantedMembersUsernames, libwekan.Username(username))
	}
	wantedMembers, err := wekan.GetUsersFromUsernames(context.Background(), wantedMembersUsernames)
	if err != nil {
		return nil, nil, err
	}
	//wantedMembersIDs := mapSlice(wantedMembers, func(user libwekan.User) libwekan.UserID { return user.ID })
	wantedUserMap := mapifySlice(wantedMembers, libwekan.User.GetID)
	return wantedUserMap, keys(wantedUserMap), err
}

func (users Users) inferBoardsMember() BoardsMembers {
	wekanBoardsUserSlice := make(map[libwekan.BoardSlug][]User)
	for _, user := range users {
		for _, boardSlug := range user.boards {
			if boardSlug != "" {
				boardSlug := libwekan.BoardSlug(boardSlug)
				wekanBoardsUserSlice[boardSlug] = append(wekanBoardsUserSlice[boardSlug], user)
			}
		}
	}

	wekanBoardsUsers := make(BoardsMembers)
	for boardSlug, userSlice := range wekanBoardsUserSlice {
		wekanBoardsUsers[boardSlug] = mapifySlice(userSlice, func(user User) Username { return user.email })
	}
	return wekanBoardsUsers
}

func (boardsMembers BoardsMembers) addBoards(boards []libwekan.Board) BoardsMembers {
	if boardsMembers == nil {
		boardsMembers = make(BoardsMembers)
	}
	for _, b := range boards {
		if _, ok := boardsMembers[b.Slug]; !ok {
			boardsMembers[b.Slug] = make(Users)
		}
	}
	return boardsMembers
}
