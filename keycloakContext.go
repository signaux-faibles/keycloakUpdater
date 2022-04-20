package main

import (
	"context"
	"github.com/Nerzal/gocloak/v11"
	"github.com/pkg/errors"
	"github.com/signaux-faibles/keycloakUpdater/v2/logger"
	"github.com/signaux-faibles/keycloakUpdater/v2/structs"
)

// KeycloakContext carry keycloak state
type KeycloakContext struct {
	API         gocloak.GoCloak
	JWT         *gocloak.JWT
	Realm       *gocloak.RealmRepresentation
	Clients     []*gocloak.Client
	Users       []*gocloak.User
	Roles       []*gocloak.Role
	ClientRoles map[string][]*gocloak.Role
}

func NewKeycloakContext(access *structs.Access) (KeycloakContext, error) {
	init, err := Init(access.Address, access.Realm, access.Username, access.Password)
	return init, err
}

// Init provides a connected keycloak context object
func Init(hostname, realm, username, password string) (KeycloakContext, error) {
	kc := KeycloakContext{}

	kc.API = gocloak.NewClient(hostname)
	var err error
	kc.JWT, err = kc.API.LoginAdmin(context.Background(), username, password, realm)
	if err != nil {
		return KeycloakContext{}, err
	}

	// fetch Realm
	kc.Realm, err = kc.API.GetRealm(context.Background(), kc.JWT.AccessToken, realm)
	if err != nil {
		return KeycloakContext{}, err
	}

	err = kc.refreshClients()
	if err != nil {
		return KeycloakContext{}, err
	}

	err = kc.refreshUsers()
	if err != nil {
		return KeycloakContext{}, err
	}

	kc.Roles, err = kc.API.GetRealmRoles(context.Background(), kc.JWT.AccessToken, realm, gocloak.GetRoleParams{})
	if err != nil {
		return KeycloakContext{}, err
	}

	err = kc.refreshClientRoles()
	if err != nil {
		return KeycloakContext{}, err
	}

	return kc, nil
}

// GetRoles returns realm roles in []string
func (kc KeycloakContext) GetRoles() Roles {
	var roles Roles
	for _, r := range kc.Roles {
		if r != nil && r.Name != nil {
			roles.add(*r.Name)
		}
	}
	return roles
}

// CreateClientRoles creates a bunch of roles in a client from a []string
func (kc *KeycloakContext) CreateClientRoles(clientID string, roles Roles) (int, error) {
	fields := logger.DataForMethod("kc.CreateClientRoles")

	defer func() {
		if err := kc.refreshClientRoles(); err != nil {
			logger.ErrorE("error refreshing client roles", fields, err)
			panic(err)
		}
	}()

	internalClientID, err := kc.GetInternalIDFromClientID(clientID)
	if err != nil {
		return 0, errors.Errorf("kc.CreateClientRoles, %s: no such client", clientID)
	}

	i := 0
	for _, role := range roles {
		if kc.GetClientRoles()[clientID].contains(role) {
			return i, errors.Errorf("kc.CreateClientRoles, %s: role already exists", role)
		}
		kcRole := gocloak.Role{
			Name: &role,
		}
		_, err := kc.API.CreateClientRole(context.Background(), kc.JWT.AccessToken, kc.getRealmName(), internalClientID, kcRole)
		if err != nil {
			return i, errors.Errorf("kc.CreateClientRoles, %s: could not create roles, %s", role, err.Error())
		}
		i++
	}
	return i, nil
}

// GetInternalIDFromClientID resolves internal client ID from human readable clientID
func (kc KeycloakContext) GetInternalIDFromClientID(clientID string) (string, error) {
	for _, c := range kc.Clients {
		if c != nil && c.ClientID != nil {
			if *c.ClientID == clientID {
				return *c.ID, nil
			}
		}
	}
	return "", errors.Errorf("kc.GetInternalIDFromClientID %s: no such clientID", clientID)
}

// GetQuietlyInternalIDFromClientID resolves internal client ID from human readable clientID
func (kc KeycloakContext) GetQuietlyInternalIDFromClientID(clientID string) (string, bool) {
	id, err := kc.GetInternalIDFromClientID(clientID)
	if err != nil {
		return "", false
	}
	return id, true
}

// GetClientRoles returns realm roles in map[string][]string
func (kc KeycloakContext) GetClientRoles() map[string]Roles {
	clientRoles := make(map[string]Roles)
	for n, c := range kc.ClientRoles {
		var roles []string
		for _, r := range c {
			roles = append(roles, *r.Name)
		}
		clientRoles[n] = roles
	}
	return clientRoles
}

func (kc *KeycloakContext) refreshClients() error {
	var err error
	kc.Clients, err = kc.API.GetClients(context.Background(), kc.JWT.AccessToken, kc.getRealmName(), gocloak.GetClientsParams{})
	return err
}

// refreshUsers pulls user base from keycloak server
func (kc *KeycloakContext) refreshUsers() error {
	var err error
	max := 100000
	kc.Users, err = kc.API.GetUsers(context.Background(), kc.JWT.AccessToken, kc.getRealmName(), gocloak.GetUsersParams{
		Max: &max,
	})
	return err
}

func (kc *KeycloakContext) refreshClientRoles() error {
	kc.ClientRoles = make(map[string][]*gocloak.Role)
	for _, c := range kc.Clients {
		if c != nil && c.ClientID != nil {
			roles, err := kc.API.GetClientRoles(context.Background(), kc.JWT.AccessToken, kc.getRealmName(), *c.ID, gocloak.GetRoleParams{})
			if err != nil {
				return err
			}
			kc.ClientRoles[*c.ClientID] = roles
		}
	}
	return nil
}

// CreateUsers sends a slice of gocloak Users to keycloak
func (kc *KeycloakContext) CreateUsers(users []gocloak.User, userMap Users, clientName string) error {
	fields := logger.DataForMethod("kc.CreateUsers")
	internalID, err := kc.GetInternalIDFromClientID(clientName)
	if err != nil {
		return err
	}
	for _, user := range users {
		fields.AddUser(user)
		logger.Info("creating user", fields)
		u, err := kc.API.CreateUser(context.Background(), kc.JWT.AccessToken, kc.getRealmName(), user)
		if err != nil {
			logger.WarnE("unable to create user", fields, err)
		}

		roles := userMap[*user.Username].roles().GetKeycloakRoles(clientName, *kc)
		gocloakRoles := rolesFromGocloakRoles(roles)
		fields.AddArray("roles", gocloakRoles)
		logger.Info("adding roles to user", fields)
		err = kc.API.AddClientRoleToUser(context.Background(), kc.JWT.AccessToken, kc.getRealmName(), internalID, u, roles)
		if err != nil {
			var rolesInError []string
			for _, r := range roles {
				rolesInError = append(rolesInError, *r.Name)
			}
			fields.AddArray("rolesInError", rolesInError)
			fields.AddAny("userEmail", *user.Email)
			logger.ErrorE("error adding client roles", fields, err)
			panic(err)
		}
	}

	err = kc.refreshUsers()
	return err
}

// DisableUsers disables users and deletes every roles of users
func (kc *KeycloakContext) DisableUsers(users []gocloak.User, clientName string) error {
	fields := logger.DataForMethod("kc.DisableUsers")
	f := false
	internalID, err := kc.GetInternalIDFromClientID(clientName)
	if err != nil {
		return err
	}
	for _, u := range users {
		u.Enabled = &f
		fields.AddUser(u)
		logger.Info("disabling user", fields)
		err := kc.API.UpdateUser(context.Background(), kc.JWT.AccessToken, kc.getRealmName(), u)
		if err != nil {
			logger.WarnE("error disabling user", fields, err)
			return err
		}
		roles, err := kc.API.GetClientRolesByUserID(context.Background(), kc.JWT.AccessToken, kc.getRealmName(), internalID, *u.ID)
		if err != nil {
			logger.WarnE("failed to retrieve roles for user", fields, err)
		}
		var ro []gocloak.Role
		for _, r := range roles {
			ro = append(ro, *r)
		}
		fields.AddArray("roles", rolesFromGocloakPRoles(roles))
		logger.Info("remove roles from user", fields)
		err = kc.API.DeleteClientRoleFromUser(context.Background(), kc.JWT.AccessToken, kc.getRealmName(), internalID, *u.ID, ro)
		if err != nil {
			logger.WarnE("failed to remove roles", fields, err)
			return err
		}
	}
	err = kc.refreshUsers()
	return err
}

// EnableUsers enables users and adds roles
func (kc *KeycloakContext) EnableUsers(users []gocloak.User) error {
	fields := logger.DataForMethod("kc.EnableUsers")
	t := true
	for _, user := range users {
		fields.AddUser(user)
		logger.Info("enabling user", fields)
		user.Enabled = &t
		err := kc.API.UpdateUser(context.Background(), kc.JWT.AccessToken, kc.getRealmName(), user)
		if err != nil {
			logger.WarnE("failed to enable user", fields, err)
		}
	}
	err := kc.refreshUsers()
	return err
}

// UpdateCurrentUsers sets client roles on specified users according userMap
func (kc KeycloakContext) UpdateCurrentUsers(users []gocloak.User, userMap Users, clientName string) error {
	fields := logger.DataForMethod("kc.UpdateCurrentUsers")
	accountInternalID, err := kc.GetInternalIDFromClientID("account")
	if err != nil {
		return err
	}
	internalID, err := kc.GetInternalIDFromClientID(clientName)
	if err != nil {
		return err
	}

	for _, user := range users {
		fields.AddUser(user)
		roles, err := kc.API.GetClientRolesByUserID(context.Background(), kc.JWT.AccessToken, kc.getRealmName(), internalID, *user.ID)
		if err != nil {
			return err
		}
		accountPRoles, err := kc.API.GetClientRolesByUserID(context.Background(), kc.JWT.AccessToken, kc.getRealmName(), accountInternalID, *user.ID)
		if err != nil {
			return err
		}
		accountRoles := rolesFromGocloakPRoles(accountPRoles)

		u := userMap[*user.Username]
		ug := u.ToGocloakUser()
		if user.LastName != nil && u.nom != *user.LastName ||
			user.LastName != nil && u.prenom != *user.FirstName ||
			!compareAttributes(user.Attributes, ug.Attributes) {

			update := gocloak.User{
				ID:         user.ID,
				FirstName:  &u.prenom,
				LastName:   &u.nom,
				Attributes: ug.Attributes,
			}
			logger.Info("updating user name and attributes", fields)
			err := kc.API.UpdateUser(context.Background(), kc.JWT.AccessToken, kc.getRealmName(), update)
			if err != nil {
				logger.WarnE("failed to update user names", fields, err)
				return err
			}
		}

		novel, old := userMap[*user.Username].roles().compare(rolesFromGocloakPRoles(roles))
		if len(old) > 0 {
			fields.AddArray("oldRoles", old)
			logger.Info("deleting unused roles", fields)
			err = kc.API.DeleteClientRoleFromUser(context.Background(), kc.JWT.AccessToken, kc.getRealmName(), internalID, *user.ID, old.GetKeycloakRoles(clientName, kc))
			if err != nil {
				logger.WarnE("failed to delete roles", fields, err)
			}
			fields.Remove("oldRoles")
		}

		if len(novel) > 0 {
			fields.AddArray("novelRoles", novel)
			logger.Info("adding missing roles", fields)
			err = kc.API.AddClientRoleToUser(context.Background(), kc.JWT.AccessToken, kc.getRealmName(), internalID, *user.ID, novel.GetKeycloakRoles(clientName, kc))
			if err != nil {
				logger.WarnE("failed to add roles", fields, err)
			}
			fields.Remove("novelRoles")
		}

		if len(accountRoles) > 0 {
			fields.AddArray("accountRoles", accountRoles)
			logger.Info("disabling account management", fields)
			err = kc.API.DeleteClientRoleFromUser(context.Background(), kc.JWT.AccessToken, kc.getRealmName(), accountInternalID, *user.ID, accountRoles.GetKeycloakRoles("account", kc))
			if err != nil {
				logger.WarnE("failed to disable management", fields, err)
			}
			fields.Remove("accountRoles")
		}
	}

	return nil
}

// SaveMasterRealm update master Realm
func (kc KeycloakContext) SaveMasterRealm(input gocloak.RealmRepresentation) {
	fields := logger.DataForMethod("kc.SaveMasterRealm")
	id := "master"
	input.ID = &id
	input.Realm = &id
	logger.Info("update realm", fields)
	if err := kc.API.UpdateRealm(context.Background(), kc.JWT.AccessToken, input); err != nil {
		logger.ErrorE("Error when updating Realm ", fields, err)
		panic(err)
	}
}

// SaveClients save clients then refresh clients list
func (kc *KeycloakContext) SaveClients(input []*gocloak.Client) {
	fields := logger.DataForMethod("kc.SaveClients")
	for _, client := range input {
		fields.AddClient(*client)
		logger.Info("save client", fields)
		kc.saveClient(*client)
	}
	err := kc.refreshClients()
	if err != nil {
		logger.ErrorE("Error refreshing clients ", fields, err)
		panic(err)
	}
}

func (kc KeycloakContext) saveClient(input gocloak.Client) {
	fields := logger.DataForMethod("kc.saveClient")
	fields.AddClient(input)
	id, found := kc.GetQuietlyInternalIDFromClientID(*input.ClientID)
	// need client creation
	fields.AddAny("found", found)
	fields.AddAny("id", id)
	if !found {
		createdId, err := kc.API.CreateClient(context.Background(), kc.JWT.AccessToken, kc.getRealmName(), input)
		if err != nil {
			logger.ErrorE("error creating client", fields, err)
			panic(err)
		}
		fields.AddAny("id", createdId)
		logger.Debug("new client is created", fields)
		return
	}
	// update client
	input.ID = &id
	if err := kc.API.UpdateClient(context.Background(), kc.JWT.AccessToken, kc.getRealmName(), input); err != nil {
		logger.ErrorE("error updating clients", fields, err)
		panic(err)
	}
}

func (kc KeycloakContext) getRealmName() string {
	return *kc.Realm.Realm
}
