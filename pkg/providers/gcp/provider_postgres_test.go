package gcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	k8sTypes "k8s.io/apimachinery/pkg/types"
	utils "k8s.io/utils/pointer"
	"reflect"
	"testing"
	"time"

	"github.com/integr8ly/cloud-resource-operator/pkg/providers/gcp/gcpiface"

	v1 "github.com/integr8ly/cloud-resource-operator/apis/config/v1"
	"github.com/integr8ly/cloud-resource-operator/apis/integreatly/v1alpha1"
	"github.com/integr8ly/cloud-resource-operator/apis/integreatly/v1alpha1/types"
	moqClient "github.com/integr8ly/cloud-resource-operator/pkg/client/fake"
	"github.com/integr8ly/cloud-resource-operator/pkg/providers"
	"github.com/sirupsen/logrus"
	sqladmin "google.golang.org/api/sqladmin/v1beta4"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	gcpTestPostgresInstanceName = "gcptestclustertestNsgcpcloudsql"
	testInfrastructureName      = "cluster"
	testUser                    = "user"
	testPassword                = "password"
)

func buildTestPostgres() *v1alpha1.Postgres {
	postgres := buildTestPostgresWithoutAnnotation()
	postgres.Annotations = map[string]string{
		ResourceIdentifierAnnotation: testName,
	}
	return postgres
}

func buildTestPostgresWithoutAnnotation() *v1alpha1.Postgres {
	return &v1alpha1.Postgres{
		ObjectMeta: metav1.ObjectMeta{
			Name:      postgresProviderName,
			Namespace: testNs,
			Labels: map[string]string{
				"productName": "test_product",
			},
			ResourceVersion: "1000",
		},
	}
}

func buildTestPostgresSecret() *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      postgresProviderName,
			Namespace: testNs,
		},
		Data: map[string][]byte{
			defaultPostgresUserKey:     []byte(testUser),
			defaultPostgresPasswordKey: []byte(testPassword),
		},
	}
}

func TestPostgresProvider_DeleteCloudSQLInstance(t *testing.T) {

	scheme, err := buildTestScheme()
	if err != nil {
		t.Fatal("failed to build scheme", err)
	}
	type fields struct {
		Client            client.Client
		CredentialManager CredentialManager
		ConfigManager     ConfigManager
		Logger            *logrus.Entry
	}
	type args struct {
		ctx             context.Context
		p               *v1alpha1.Postgres
		sqladminService *gcpiface.MockSqlClient
		networkManager  NetworkManager
		isLastResource  bool
		projectID       string
	}

	tests := []struct {
		name    string
		fields  fields
		args    args
		want    types.StatusMessage
		wantErr bool
	}{
		{
			name: "if instance is not nil and state is PENDING_DELETE return status message",
			fields: fields{
				Client: moqClient.NewSigsClientMoqWithScheme(scheme, buildTestPostgresSecret(), buildTestPostgres(), buildTestGcpInfrastructure(nil)),
				CredentialManager: &CredentialManagerMock{
					ReconcileProviderCredentialsFunc: func(ctx context.Context, ns string) (*Credentials, error) {
						return &Credentials{}, nil
					},
				},
				ConfigManager: &ConfigManagerMock{
					ReadStorageStrategyFunc: func(ctx context.Context, rt providers.ResourceType, tier string) (*StrategyConfig, error) {
						return &StrategyConfig{
							Region:         gcpTestRegion,
							ProjectID:      gcpTestProjectId,
							CreateStrategy: json.RawMessage(`{}`),
							DeleteStrategy: json.RawMessage(`{}`),
						}, nil
					},
				},
				Logger: logrus.NewEntry(logrus.StandardLogger()),
			},
			args: args{
				ctx:            context.TODO(),
				p:              buildTestPostgres(),
				networkManager: buildMockNetworkManager(),
				sqladminService: gcpiface.GetMockSQLClient(func(sqlClient *gcpiface.MockSqlClient) {
					sqlClient.GetInstanceFn = func(ctx context.Context, s string, s2 string) (*sqladmin.DatabaseInstance, error) {
						return &sqladmin.DatabaseInstance{
							Name:  gcpTestPostgresInstanceName,
							State: "PENDING_DELETE",
						}, nil
					}
				}),
				isLastResource: false,
				projectID:      gcpTestProjectId,
			},
			want:    "postgres instance " + gcpTestPostgresInstanceName + " is already deleting",
			wantErr: false,
		},

		{
			name: "if instance is not nil, delete is not in progress delete function returns error",
			fields: fields{
				Client: moqClient.NewSigsClientMoqWithScheme(scheme, buildTestPostgresSecret(), buildTestPostgres(), buildTestGcpInfrastructure(nil)),
				ConfigManager: &ConfigManagerMock{
					ReadStorageStrategyFunc: func(ctx context.Context, rt providers.ResourceType, tier string) (*StrategyConfig, error) {
						return &StrategyConfig{
							Region:         gcpTestRegion,
							ProjectID:      gcpTestProjectId,
							CreateStrategy: json.RawMessage(`{}`),
							DeleteStrategy: json.RawMessage(`{}`),
						}, nil
					},
				},
				Logger: logrus.NewEntry(logrus.StandardLogger()),
			},
			args: args{
				ctx:            context.TODO(),
				p:              buildTestPostgres(),
				networkManager: buildMockNetworkManager(),
				sqladminService: gcpiface.GetMockSQLClient(func(sqlClient *gcpiface.MockSqlClient) {
					sqlClient.GetInstanceFn = func(ctx context.Context, s string, s2 string) (*sqladmin.DatabaseInstance, error) {
						return &sqladmin.DatabaseInstance{
							Name:     gcpTestPostgresInstanceName,
							State:    "RUNNABLE",
							Settings: &sqladmin.Settings{DeletionProtectionEnabled: false},
						}, nil
					}
					sqlClient.DeleteInstanceFn = func(ctx context.Context, s string, s2 string) (*sqladmin.Operation, error) {
						return nil, errors.New("failed to delete cloudSQL instance: " + gcpTestPostgresInstanceName)
					}
				}),
				isLastResource: false,
				projectID:      gcpTestProjectId,
			},
			want:    "failed to delete postgres instance: " + gcpTestPostgresInstanceName,
			wantErr: true,
		},
		{
			name: "error when getting cloud sql instance",
			fields: fields{
				Client: moqClient.NewSigsClientMoqWithScheme(scheme, buildTestPostgresSecret(), buildTestPostgres(), buildTestGcpInfrastructure(nil)),
				ConfigManager: &ConfigManagerMock{
					ReadStorageStrategyFunc: func(ctx context.Context, rt providers.ResourceType, tier string) (*StrategyConfig, error) {
						return &StrategyConfig{
							Region:         gcpTestRegion,
							ProjectID:      gcpTestProjectId,
							CreateStrategy: json.RawMessage(`{}`),
							DeleteStrategy: json.RawMessage(`{}`),
						}, nil
					},
				},
				Logger: logrus.NewEntry(logrus.StandardLogger()),
			},
			args: args{
				ctx:            context.TODO(),
				p:              buildTestPostgres(),
				networkManager: buildMockNetworkManager(),
				sqladminService: gcpiface.GetMockSQLClient(func(sqlClient *gcpiface.MockSqlClient) {
					sqlClient.GetInstanceFn = func(ctx context.Context, s string, s2 string) (*sqladmin.DatabaseInstance, error) {
						return nil, fmt.Errorf("cannot retrieve sql instance from gcp")
					}
					sqlClient.InstancesListFn = func(s string) (*sqladmin.InstancesListResponse, error) {
						return &sqladmin.InstancesListResponse{
							Items: []*sqladmin.DatabaseInstance{
								{},
							},
						}, fmt.Errorf("cannot retrieve sql instances from gcp")
					}
				}),
				isLastResource: false,
				projectID:      gcpTestProjectId,
			},
			want:    "cannot retrieve sql instance from gcp",
			wantErr: true,
		},
		{
			name: "failed to retrieve postgres strategy config",
			fields: fields{
				Client: func() client.Client {
					mc := moqClient.NewSigsClientMoqWithScheme(scheme, buildTestPostgresSecret(), buildTestGcpInfrastructure(nil))
					mc.GetFunc = func(ctx context.Context, key k8sTypes.NamespacedName, obj client.Object) error {
						return fmt.Errorf("failed to retrieve postgres strategy config")
					}
					return mc
				}(),
				ConfigManager: &ConfigManagerMock{
					ReadStorageStrategyFunc: func(ctx context.Context, rt providers.ResourceType, tier string) (*StrategyConfig, error) {
						return &StrategyConfig{
							Region:         gcpTestRegion,
							ProjectID:      gcpTestProjectId,
							CreateStrategy: json.RawMessage(`{}`),
							DeleteStrategy: json.RawMessage(`{}`),
						}, nil
					},
				},
				Logger: logrus.NewEntry(logrus.StandardLogger()),
			},
			args: args{
				ctx:             context.TODO(),
				p:               buildTestPostgres(),
				networkManager:  buildMockNetworkManager(),
				sqladminService: gcpiface.GetMockSQLClient(nil),
				isLastResource:  false,
			},
			want:    "failed to retrieve postgres strategy config",
			wantErr: true,
		},
		{
			name: "Error deleting cloudSQL secrets",
			fields: fields{
				Client: func() client.Client {
					mc := moqClient.NewSigsClientMoqWithScheme(scheme, buildTestPostgresSecret(), buildTestGcpInfrastructure(nil))
					mc.DeleteFunc = func(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error {
						return fmt.Errorf("generic error")
					}
					return mc
				}(),
				ConfigManager: &ConfigManagerMock{
					ReadStorageStrategyFunc: func(ctx context.Context, rt providers.ResourceType, tier string) (*StrategyConfig, error) {
						return &StrategyConfig{
							Region:         gcpTestRegion,
							ProjectID:      gcpTestProjectId,
							CreateStrategy: json.RawMessage(`{}`),
							DeleteStrategy: json.RawMessage(`{}`),
						}, nil
					},
				},
				Logger: logrus.NewEntry(logrus.StandardLogger()),
			},
			args: args{
				ctx:             context.TODO(),
				p:               buildTestPostgres(),
				networkManager:  buildMockNetworkManager(),
				sqladminService: gcpiface.GetMockSQLClient(nil),
				isLastResource:  false,
				projectID:       gcpTestProjectId,
			},
			want:    "failed to delete cloudSQL secrets",
			wantErr: true,
		},
		{
			name: "successful run of delete function when cloudsql object is already deleted",
			fields: fields{
				Client: moqClient.NewSigsClientMoqWithScheme(scheme, buildTestPostgresSecret(), buildTestPostgres(), buildTestGcpInfrastructure(nil)),
				ConfigManager: &ConfigManagerMock{
					ReadStorageStrategyFunc: func(ctx context.Context, rt providers.ResourceType, tier string) (*StrategyConfig, error) {
						return &StrategyConfig{
							Region:         gcpTestRegion,
							ProjectID:      gcpTestProjectId,
							CreateStrategy: json.RawMessage(`{}`),
							DeleteStrategy: json.RawMessage(`{}`),
						}, nil
					},
				},
				Logger: logrus.NewEntry(logrus.StandardLogger()),
			},
			args: args{
				ctx:             context.TODO(),
				p:               buildTestPostgres(),
				networkManager:  buildMockNetworkManager(),
				sqladminService: gcpiface.GetMockSQLClient(nil),
				isLastResource:  false,
				projectID:       gcpTestProjectId,
			},
			want:    "",
			wantErr: false,
		},
		{
			name: "successful run of delete function when cloudsql object is not already deleted",
			fields: fields{
				Client: moqClient.NewSigsClientMoqWithScheme(scheme, buildTestPostgresSecret(), buildTestPostgres(), buildTestGcpInfrastructure(nil)),
				ConfigManager: &ConfigManagerMock{
					ReadStorageStrategyFunc: func(ctx context.Context, rt providers.ResourceType, tier string) (*StrategyConfig, error) {
						return &StrategyConfig{
							Region:         gcpTestRegion,
							ProjectID:      gcpTestProjectId,
							CreateStrategy: json.RawMessage(`{}`),
							DeleteStrategy: json.RawMessage(`{}`),
						}, nil
					},
				},
				Logger: logrus.NewEntry(logrus.StandardLogger()),
			},
			args: args{
				ctx:            context.TODO(),
				p:              buildTestPostgres(),
				networkManager: buildMockNetworkManager(),
				sqladminService: gcpiface.GetMockSQLClient(func(sqlClient *gcpiface.MockSqlClient) {
					sqlClient.GetInstanceFn = func(ctx context.Context, s string, s2 string) (*sqladmin.DatabaseInstance, error) {
						return &sqladmin.DatabaseInstance{
							Name:     gcpTestPostgresInstanceName,
							State:    "RUNNABLE",
							Settings: &sqladmin.Settings{DeletionProtectionEnabled: false},
						}, nil
					}
				}),
				isLastResource: false,
				projectID:      gcpTestProjectId,
			},
			want:    "delete detected, Instances.Delete() started",
			wantErr: false,
		},
		{
			name: "want error when running delete function when cloudsql object is not already deleted but delete errors",
			fields: fields{
				Client: moqClient.NewSigsClientMoqWithScheme(scheme, buildTestPostgresSecret(), buildTestPostgres(), buildTestGcpInfrastructure(nil)),
				ConfigManager: &ConfigManagerMock{
					ReadStorageStrategyFunc: func(ctx context.Context, rt providers.ResourceType, tier string) (*StrategyConfig, error) {
						return &StrategyConfig{
							Region:         gcpTestRegion,
							ProjectID:      gcpTestProjectId,
							CreateStrategy: json.RawMessage(`{}`),
							DeleteStrategy: json.RawMessage(`{}`),
						}, nil
					},
				},
				Logger: logrus.NewEntry(logrus.StandardLogger()),
			},
			args: args{
				ctx:            context.TODO(),
				p:              buildTestPostgres(),
				networkManager: buildMockNetworkManager(),
				sqladminService: gcpiface.GetMockSQLClient(func(sqlClient *gcpiface.MockSqlClient) {
					sqlClient.GetInstanceFn = func(ctx context.Context, s string, s2 string) (*sqladmin.DatabaseInstance, error) {
						return &sqladmin.DatabaseInstance{
							Name:     gcpTestPostgresInstanceName,
							State:    "RUNNABLE",
							Settings: &sqladmin.Settings{DeletionProtectionEnabled: false},
						}, nil
					}
					sqlClient.DeleteInstanceFn = func(ctx context.Context, s string, s2 string) (*sqladmin.Operation, error) {
						return nil, errors.New("delete error")
					}
				}),
				isLastResource: false,
				projectID:      gcpTestProjectId,
			},
			want:    "failed to delete postgres instance: " + gcpTestPostgresInstanceName,
			wantErr: true,
		},
		{
			name: "Error failed to update instance as part of finalizer reconcile",
			fields: fields{
				Client: func() client.Client {
					mc := moqClient.NewSigsClientMoqWithScheme(scheme, buildTestGcpInfrastructure(nil))
					mc.UpdateFunc = func(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error {
						return fmt.Errorf("generic error")
					}
					return mc
				}(),
				ConfigManager: &ConfigManagerMock{
					ReadStorageStrategyFunc: func(ctx context.Context, rt providers.ResourceType, tier string) (*StrategyConfig, error) {
						return &StrategyConfig{
							Region:         gcpTestRegion,
							ProjectID:      gcpTestProjectId,
							CreateStrategy: json.RawMessage(`{}`),
							DeleteStrategy: json.RawMessage(`{}`),
						}, nil
					},
				},
				Logger: logrus.NewEntry(logrus.StandardLogger()),
			},
			args: args{
				ctx: context.TODO(),
				p: &v1alpha1.Postgres{
					ObjectMeta: metav1.ObjectMeta{
						Name:      postgresProviderName,
						Namespace: testNs,
						Annotations: map[string]string{
							ResourceIdentifierAnnotation: testName,
						},
					},
				},
				networkManager:  buildMockNetworkManager(),
				sqladminService: gcpiface.GetMockSQLClient(nil),
				isLastResource:  false,
				projectID:       gcpTestProjectId,
			},
			want:    "failed to update instance as part of finalizer reconcile",
			wantErr: true,
		},
		{
			name: "error when modifying cloud sql instances",
			fields: fields{
				Client: moqClient.NewSigsClientMoqWithScheme(scheme, buildTestPostgresSecret(), buildTestPostgres(), buildTestGcpInfrastructure(nil)),
				ConfigManager: &ConfigManagerMock{
					ReadStorageStrategyFunc: func(ctx context.Context, rt providers.ResourceType, tier string) (*StrategyConfig, error) {
						return &StrategyConfig{
							Region:         gcpTestRegion,
							ProjectID:      gcpTestProjectId,
							CreateStrategy: json.RawMessage(`{}`),
							DeleteStrategy: json.RawMessage(`{}`),
						}, nil
					},
				},
				Logger: logrus.NewEntry(logrus.StandardLogger()),
			},
			args: args{
				ctx:            context.TODO(),
				p:              buildTestPostgres(),
				networkManager: buildMockNetworkManager(),
				sqladminService: gcpiface.GetMockSQLClient(func(sqlClient *gcpiface.MockSqlClient) {
					sqlClient.GetInstanceFn = func(ctx context.Context, s string, s2 string) (*sqladmin.DatabaseInstance, error) {
						return &sqladmin.DatabaseInstance{
							Name:     gcpTestPostgresInstanceName,
							State:    "RUNNABLE",
							Settings: &sqladmin.Settings{DeletionProtectionEnabled: true},
						}, nil
					}
					sqlClient.ModifyInstanceFn = func(ctx context.Context, s string, s2 string, instance *sqladmin.DatabaseInstance) (*sqladmin.Operation, error) {
						return nil, fmt.Errorf("failed to modify cloudsql instance")
					}
				}),
				isLastResource: false,
			},
			want:    "failed to modify cloudsql instance: " + gcpTestPostgresInstanceName,
			wantErr: true,
		},
		{
			name: "failed to delete cluster network peering",
			fields: fields{
				Client: moqClient.NewSigsClientMoqWithScheme(scheme, buildTestPostgresSecret(), buildTestPostgres(), buildTestGcpInfrastructure(nil)),
				CredentialManager: &CredentialManagerMock{
					ReconcileProviderCredentialsFunc: func(ctx context.Context, ns string) (*Credentials, error) {
						return &Credentials{}, nil
					},
				},
				ConfigManager: &ConfigManagerMock{
					ReadStorageStrategyFunc: func(ctx context.Context, rt providers.ResourceType, tier string) (*StrategyConfig, error) {
						return &StrategyConfig{
							Region:         gcpTestRegion,
							ProjectID:      gcpTestProjectId,
							CreateStrategy: json.RawMessage(`{}`),
							DeleteStrategy: json.RawMessage(`{}`),
						}, nil
					},
				},
				Logger: logrus.NewEntry(logrus.StandardLogger()),
			},
			args: args{
				ctx: context.TODO(),
				p: &v1alpha1.Postgres{
					ObjectMeta: metav1.ObjectMeta{
						Name:      postgresProviderName,
						Namespace: testNs,
						Annotations: map[string]string{
							ResourceIdentifierAnnotation: testName,
						},
					},
				},
				networkManager: &NetworkManagerMock{
					DeleteNetworkPeeringFunc: func(contextMoqParam context.Context) error {
						return fmt.Errorf("generic error")
					},
				},
				sqladminService: gcpiface.GetMockSQLClient(func(sqlClient *gcpiface.MockSqlClient) {
					sqlClient.InstancesListFn = func(s string) (*sqladmin.InstancesListResponse, error) {
						return &sqladmin.InstancesListResponse{
							Items: []*sqladmin.DatabaseInstance{},
						}, nil
					}
					sqlClient.DeleteInstanceFn = func(ctx context.Context, s string, s2 string) (*sqladmin.Operation, error) {
						return &sqladmin.Operation{}, nil
					}
				}),
				isLastResource: true,
			},
			want:    "failed to delete cluster network peering",
			wantErr: true,
		},
		{
			name: "failed to delete cluster network service",
			fields: fields{
				Client: moqClient.NewSigsClientMoqWithScheme(scheme, buildTestPostgresSecret(), buildTestPostgres(), buildTestGcpInfrastructure(nil)),
				CredentialManager: &CredentialManagerMock{
					ReconcileProviderCredentialsFunc: func(ctx context.Context, ns string) (*Credentials, error) {
						return &Credentials{}, nil
					},
				},
				ConfigManager: &ConfigManagerMock{
					ReadStorageStrategyFunc: func(ctx context.Context, rt providers.ResourceType, tier string) (*StrategyConfig, error) {
						return &StrategyConfig{
							Region:         gcpTestRegion,
							ProjectID:      gcpTestProjectId,
							CreateStrategy: json.RawMessage(`{}`),
							DeleteStrategy: json.RawMessage(`{}`),
						}, nil
					},
				},
				Logger: logrus.NewEntry(logrus.StandardLogger()),
			},
			args: args{
				ctx: context.TODO(),
				p: &v1alpha1.Postgres{
					ObjectMeta: metav1.ObjectMeta{
						Name:      postgresProviderName,
						Namespace: testNs,
						Annotations: map[string]string{
							ResourceIdentifierAnnotation: testName,
						},
					},
				},
				networkManager: &NetworkManagerMock{
					DeleteNetworkPeeringFunc: func(contextMoqParam context.Context) error {
						return nil
					},
					DeleteNetworkServiceFunc: func(contextMoqParam context.Context) error {
						return fmt.Errorf("generic error")
					},
				},
				sqladminService: gcpiface.GetMockSQLClient(func(sqlClient *gcpiface.MockSqlClient) {
					sqlClient.InstancesListFn = func(s string) (*sqladmin.InstancesListResponse, error) {
						return &sqladmin.InstancesListResponse{
							Items: []*sqladmin.DatabaseInstance{},
						}, nil
					}
					sqlClient.DeleteInstanceFn = func(ctx context.Context, s string, s2 string) (*sqladmin.Operation, error) {
						return &sqladmin.Operation{}, nil
					}
				}),
				isLastResource: true,
			},
			want:    "failed to delete cluster network service",
			wantErr: true,
		},
		{
			name: "failed to delete network IP range",
			fields: fields{
				Client: moqClient.NewSigsClientMoqWithScheme(scheme, buildTestPostgresSecret(), buildTestPostgres(), buildTestGcpInfrastructure(nil)),
				CredentialManager: &CredentialManagerMock{
					ReconcileProviderCredentialsFunc: func(ctx context.Context, ns string) (*Credentials, error) {
						return &Credentials{}, nil
					},
				},
				ConfigManager: &ConfigManagerMock{
					ReadStorageStrategyFunc: func(ctx context.Context, rt providers.ResourceType, tier string) (*StrategyConfig, error) {
						return &StrategyConfig{
							Region:         gcpTestRegion,
							ProjectID:      gcpTestProjectId,
							CreateStrategy: json.RawMessage(`{}`),
							DeleteStrategy: json.RawMessage(`{}`),
						}, nil
					},
				},
				Logger: logrus.NewEntry(logrus.StandardLogger()),
			},
			args: args{
				ctx: context.TODO(),
				p: &v1alpha1.Postgres{
					ObjectMeta: metav1.ObjectMeta{
						Name:      postgresProviderName,
						Namespace: testNs,
						Annotations: map[string]string{
							ResourceIdentifierAnnotation: testName,
						},
					},
				},
				networkManager: &NetworkManagerMock{
					DeleteNetworkPeeringFunc: func(contextMoqParam context.Context) error {
						return nil
					},
					DeleteNetworkServiceFunc: func(contextMoqParam context.Context) error {
						return nil
					},
					DeleteNetworkIpRangeFunc: func(contextMoqParam context.Context) error {
						return fmt.Errorf("generic error")
					},
				},
				sqladminService: gcpiface.GetMockSQLClient(func(sqlClient *gcpiface.MockSqlClient) {
					sqlClient.InstancesListFn = func(s string) (*sqladmin.InstancesListResponse, error) {
						return &sqladmin.InstancesListResponse{
							Items: []*sqladmin.DatabaseInstance{},
						}, nil
					}
					sqlClient.DeleteInstanceFn = func(ctx context.Context, s string, s2 string) (*sqladmin.Operation, error) {
						return &sqladmin.Operation{}, nil
					}
				}),
				isLastResource: true,
			},
			want:    "failed to delete network IP range",
			wantErr: true,
		},
		{
			name: "when network component deletion in progress return status message",
			fields: fields{
				Client: moqClient.NewSigsClientMoqWithScheme(scheme, buildTestPostgresSecret(), buildTestPostgres(), buildTestGcpInfrastructure(nil)),
				CredentialManager: &CredentialManagerMock{
					ReconcileProviderCredentialsFunc: func(ctx context.Context, ns string) (*Credentials, error) {
						return &Credentials{}, nil
					},
				},
				ConfigManager: &ConfigManagerMock{
					ReadStorageStrategyFunc: func(ctx context.Context, rt providers.ResourceType, tier string) (*StrategyConfig, error) {
						return &StrategyConfig{
							Region:         gcpTestRegion,
							ProjectID:      gcpTestProjectId,
							CreateStrategy: json.RawMessage(`{}`),
							DeleteStrategy: json.RawMessage(`{}`),
						}, nil
					},
				},
				Logger: logrus.NewEntry(logrus.StandardLogger()),
			},
			args: args{
				ctx: context.TODO(),
				p: &v1alpha1.Postgres{
					ObjectMeta: metav1.ObjectMeta{
						Name:      postgresProviderName,
						Namespace: testNs,
						Annotations: map[string]string{
							ResourceIdentifierAnnotation: testName,
						},
					},
				},
				networkManager: &NetworkManagerMock{
					DeleteNetworkPeeringFunc: func(contextMoqParam context.Context) error {
						return nil
					},
					DeleteNetworkServiceFunc: func(contextMoqParam context.Context) error {
						return nil
					},
					DeleteNetworkIpRangeFunc: func(contextMoqParam context.Context) error {
						return nil
					},
					ComponentsExistFunc: func(contextMoqParam context.Context) (bool, error) {
						return true, nil
					},
				},
				sqladminService: gcpiface.GetMockSQLClient(func(sqlClient *gcpiface.MockSqlClient) {
					sqlClient.InstancesListFn = func(s string) (*sqladmin.InstancesListResponse, error) {
						return &sqladmin.InstancesListResponse{
							Items: []*sqladmin.DatabaseInstance{},
						}, nil
					}
					sqlClient.DeleteInstanceFn = func(ctx context.Context, s string, s2 string) (*sqladmin.Operation, error) {
						return &sqladmin.Operation{}, nil
					}
				}),
				isLastResource: true,
			},
			want:    "network component deletion in progress",
			wantErr: false,
		},
		{
			name: "failed to check if components exist",
			fields: fields{
				Client: moqClient.NewSigsClientMoqWithScheme(scheme, buildTestPostgresSecret(), buildTestPostgres(), buildTestGcpInfrastructure(nil)),
				CredentialManager: &CredentialManagerMock{
					ReconcileProviderCredentialsFunc: func(ctx context.Context, ns string) (*Credentials, error) {
						return &Credentials{}, nil
					},
				},
				ConfigManager: &ConfigManagerMock{
					ReadStorageStrategyFunc: func(ctx context.Context, rt providers.ResourceType, tier string) (*StrategyConfig, error) {
						return &StrategyConfig{
							Region:         gcpTestRegion,
							ProjectID:      gcpTestProjectId,
							CreateStrategy: json.RawMessage(`{}`),
							DeleteStrategy: json.RawMessage(`{}`),
						}, nil
					},
				},
				Logger: logrus.NewEntry(logrus.StandardLogger()),
			},
			args: args{
				ctx: context.TODO(),
				p: &v1alpha1.Postgres{
					ObjectMeta: metav1.ObjectMeta{
						Name:      postgresProviderName,
						Namespace: testNs,
						Annotations: map[string]string{
							ResourceIdentifierAnnotation: testName,
						},
					},
				},
				networkManager: &NetworkManagerMock{
					DeleteNetworkPeeringFunc: func(contextMoqParam context.Context) error {
						return nil
					},
					DeleteNetworkServiceFunc: func(contextMoqParam context.Context) error {
						return nil
					},
					DeleteNetworkIpRangeFunc: func(contextMoqParam context.Context) error {
						return nil
					},
					ComponentsExistFunc: func(contextMoqParam context.Context) (bool, error) {
						return false, fmt.Errorf("generic error")
					},
				},
				sqladminService: gcpiface.GetMockSQLClient(func(sqlClient *gcpiface.MockSqlClient) {
					sqlClient.InstancesListFn = func(s string) (*sqladmin.InstancesListResponse, error) {
						return &sqladmin.InstancesListResponse{
							Items: []*sqladmin.DatabaseInstance{},
						}, nil
					}
					sqlClient.DeleteInstanceFn = func(ctx context.Context, s string, s2 string) (*sqladmin.Operation, error) {
						return &sqladmin.Operation{}, nil
					}
				}),
				isLastResource: true,
			},
			want:    "failed to check if components exist",
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pp := PostgresProvider{
				Client:            tt.fields.Client,
				Logger:            tt.fields.Logger,
				CredentialManager: tt.fields.CredentialManager,
				ConfigManager:     tt.fields.ConfigManager,
			}
			got, err := pp.deleteCloudSQLInstance(tt.args.ctx, tt.args.networkManager, tt.args.sqladminService, tt.args.p, tt.args.isLastResource)
			if (err != nil) != tt.wantErr {
				t.Errorf("DeleteCloudSQLInstance() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("DeleteCloudSQLInstance() statusMessage = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPostgresProvider_GetName(t *testing.T) {
	tests := []struct {
		name string
		want string
	}{
		{
			name: "success getting postgres provider name",
			want: postgresProviderName,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pp := PostgresProvider{}
			if got := pp.GetName(); got != tt.want {
				t.Errorf("GetName() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPostgresProvider_SupportsStrategy(t *testing.T) {
	type args struct {
		deploymentStrategy string
	}
	tests := []struct {
		name string
		args args
		want bool
	}{
		{
			name: "postgres provider supports strategy",
			args: args{
				deploymentStrategy: providers.GCPDeploymentStrategy,
			},
			want: true,
		},
		{
			name: "postgres provider does not support strategy",
			args: args{
				deploymentStrategy: providers.AWSDeploymentStrategy,
			},
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pp := PostgresProvider{}
			if got := pp.SupportsStrategy(tt.args.deploymentStrategy); got != tt.want {
				t.Errorf("SupportsStrategy() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPostgresProvider_GetReconcileTime(t *testing.T) {
	type args struct {
		p *v1alpha1.Postgres
	}
	tests := []struct {
		name string
		args args
		want time.Duration
	}{
		{
			name: "get postgres default reconcile time",
			args: args{
				p: &v1alpha1.Postgres{
					Status: types.ResourceTypeStatus{
						Phase: types.PhaseComplete,
					},
				},
			},
			want: defaultReconcileTime,
		},
		{
			name: "get postgres non-default reconcile time",
			args: args{
				p: &v1alpha1.Postgres{
					Status: types.ResourceTypeStatus{
						Phase: types.PhaseInProgress,
					},
				},
			},
			want: time.Second * 60,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pp := PostgresProvider{}
			if got := pp.GetReconcileTime(tt.args.p); got != tt.want {
				t.Errorf("GetReconcileTime() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPostgresProvider_setPostgresDeletionTimestampMetric(t *testing.T) {
	scheme, err := buildTestScheme()
	if err != nil {
		t.Fatal("failed to build scheme", err)
	}
	now := time.Now()
	type fields struct {
		Client            client.Client
		Logger            *logrus.Entry
		CredentialManager CredentialManager
		ConfigManager     ConfigManager
	}
	type args struct {
		ctx             context.Context
		p               *v1alpha1.Postgres
		sqladminService *gcpiface.MockSqlClient
	}
	tests := []struct {
		name    string
		fields  fields
		args    args
		want    types.StatusMessage
		wantErr bool
	}{
		{
			name: "Deletion timestamp does exist",
			fields: fields{
				Client: moqClient.NewSigsClientMoqWithScheme(scheme, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{
					Name:      postgresProviderName + defaultCredSecSuffix,
					Namespace: testNs,
				},
				},
					&v1.Infrastructure{
						ObjectMeta: metav1.ObjectMeta{
							Name: testInfrastructureName,
						},
					},
				),
				Logger: logrus.NewEntry(logrus.StandardLogger()),
			},
			args: args{
				ctx: context.TODO(),
				p: &v1alpha1.Postgres{
					ObjectMeta: metav1.ObjectMeta{
						Name:      postgresProviderName,
						Namespace: testNs,
						Annotations: map[string]string{
							ResourceIdentifierAnnotation: testName,
						},
						DeletionTimestamp: &metav1.Time{Time: now},
					},
				},
				sqladminService: gcpiface.GetMockSQLClient(nil),
			},
			want:    "",
			wantErr: false,
		},
		{
			name: "want error when no annotation on postgres cr",
			fields: fields{
				Client: moqClient.NewSigsClientMoqWithScheme(scheme, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{
					Name:      postgresProviderName + defaultCredSecSuffix,
					Namespace: testNs,
				},
				},
					&v1.Infrastructure{
						ObjectMeta: metav1.ObjectMeta{
							Name: testInfrastructureName,
						},
						Status: v1.InfrastructureStatus{
							InfrastructureName: testInfrastructureName,
						},
					},
				),
				Logger: logrus.NewEntry(logrus.StandardLogger()),
			},
			args: args{
				ctx: context.TODO(),
				p: &v1alpha1.Postgres{
					ObjectMeta: metav1.ObjectMeta{
						Name:              postgresProviderName,
						Namespace:         testNs,
						DeletionTimestamp: &metav1.Time{Time: now},
					},
				},
				sqladminService: gcpiface.GetMockSQLClient(nil),
			},
			want:    "unable to find instance name from annotation",
			wantErr: true,
		},
		{
			name: "annotation found on postgres cr",
			fields: fields{
				Client: moqClient.NewSigsClientMoqWithScheme(scheme, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{
					Name:      postgresProviderName + defaultCredSecSuffix,
					Namespace: testNs,
				},
				},
					&v1.Infrastructure{
						ObjectMeta: metav1.ObjectMeta{
							Name: testInfrastructureName,
						},
						Status: v1.InfrastructureStatus{
							InfrastructureName: testInfrastructureName,
						},
					},
				),
				Logger: logrus.NewEntry(logrus.StandardLogger()),
			},
			args: args{
				ctx: context.TODO(),
				p: &v1alpha1.Postgres{
					ObjectMeta: metav1.ObjectMeta{
						Name:      postgresProviderName,
						Namespace: testNs,
						Annotations: map[string]string{
							ResourceIdentifierAnnotation: testName,
						},
						DeletionTimestamp: &metav1.Time{Time: now},
					},
				},
				sqladminService: gcpiface.GetMockSQLClient(nil),
			},
			want:    "",
			wantErr: false,
		},
		{
			name: "successfully retrieved cluster ID",
			fields: fields{
				Client: moqClient.NewSigsClientMoqWithScheme(scheme, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{
					Name:      postgresProviderName + defaultCredSecSuffix,
					Namespace: testNs,
				},
				},
					&v1alpha1.Postgres{
						ObjectMeta: metav1.ObjectMeta{
							Name:      postgresProviderName,
							Namespace: testNs,
							Annotations: map[string]string{
								ResourceIdentifierAnnotation: testName,
							},
						},
					},
					&v1.Infrastructure{
						ObjectMeta: metav1.ObjectMeta{
							Name: testInfrastructureName,
						},
						Status: v1.InfrastructureStatus{
							InfrastructureName: testInfrastructureName,
						},
					},
				),
				Logger: logrus.NewEntry(logrus.StandardLogger()),
			},
			args: args{
				ctx: context.TODO(),
				p: &v1alpha1.Postgres{
					ObjectMeta: metav1.ObjectMeta{
						Name:      postgresProviderName,
						Namespace: testNs,
						Annotations: map[string]string{
							ResourceIdentifierAnnotation: testName,
						},
						DeletionTimestamp: &metav1.Time{Time: now},
					},
				},
				sqladminService: gcpiface.GetMockSQLClient(nil),
			},
			want:    "",
			wantErr: false,
		},
		{
			name: "failed to get cluster ID",
			fields: fields{
				Client: moqClient.NewSigsClientMoqWithScheme(scheme, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{
					Name:      postgresProviderName + defaultCredSecSuffix,
					Namespace: testNs,
				},
				},
					&v1alpha1.Postgres{
						ObjectMeta: metav1.ObjectMeta{
							Name:      postgresProviderName,
							Namespace: testNs,
							Annotations: map[string]string{
								ResourceIdentifierAnnotation: testName,
							},
						},
					},
				),
				Logger: logrus.NewEntry(logrus.StandardLogger()),
			},
			args: args{
				ctx: context.TODO(),
				p: &v1alpha1.Postgres{
					ObjectMeta: metav1.ObjectMeta{
						Name:      postgresProviderName,
						Namespace: testNs,
						Annotations: map[string]string{
							ResourceIdentifierAnnotation: testName,
						},
						DeletionTimestamp: &metav1.Time{Time: now},
					},
				},
				sqladminService: gcpiface.GetMockSQLClient(nil),
			},
			want:    "failed to get cluster id while exposing information metric for " + gcpTestPostgresInstanceName,
			wantErr: true,
		},
		{
			name: "build postgres status metrics label successful",
			fields: fields{
				Client: moqClient.NewSigsClientMoqWithScheme(scheme, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{
					Name:      postgresProviderName + defaultCredSecSuffix,
					Namespace: testNs,
				},
				},
					&v1alpha1.Postgres{
						ObjectMeta: metav1.ObjectMeta{
							Name:      postgresProviderName,
							Namespace: testNs,
							Annotations: map[string]string{
								ResourceIdentifierAnnotation: testName,
							},
							Labels: map[string]string{
								"clusterID": "cluster",
							},
						},
						Status: types.ResourceTypeStatus{
							Phase: types.PhaseComplete,
						},
					},
					&v1.Infrastructure{
						ObjectMeta: metav1.ObjectMeta{
							Name: testInfrastructureName,
						},
						Status: v1.InfrastructureStatus{
							InfrastructureName: testInfrastructureName,
						},
					},
				),
				Logger: logrus.NewEntry(logrus.StandardLogger()),
			},
			args: args{
				ctx: context.TODO(),
				p: &v1alpha1.Postgres{
					ObjectMeta: metav1.ObjectMeta{
						Name:      postgresProviderName,
						Namespace: testNs,
						Annotations: map[string]string{
							ResourceIdentifierAnnotation: testName,
						},
						DeletionTimestamp: &metav1.Time{Time: now},
					},
				},
				sqladminService: gcpiface.GetMockSQLClient(nil),
			},
			want:    "",
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pp := &PostgresProvider{
				Client:            tt.fields.Client,
				Logger:            tt.fields.Logger,
				CredentialManager: tt.fields.CredentialManager,
				ConfigManager:     tt.fields.ConfigManager,
			}
			pp.setPostgresDeletionTimestampMetric(tt.args.ctx, tt.args.p)
		})
	}
}

func TestPostgresProvider_DeletePostgres(t *testing.T) {

	scheme, err := buildTestScheme()
	if err != nil {
		t.Fatal("failed to build scheme", err)
	}
	now := time.Now()
	type fields struct {
		Client            client.Client
		Logger            *logrus.Entry
		CredentialManager CredentialManager
		ConfigManager     ConfigManager
	}
	type args struct {
		ctx             context.Context
		p               *v1alpha1.Postgres
		sqladminService *gcpiface.MockSqlClient
	}
	tests := []struct {
		name    string
		fields  fields
		args    args
		want    types.StatusMessage
		wantErr bool
	}{
		{
			name: "failed to reconcile gcp postgres provider credentials for postgres instance",
			fields: fields{
				Client: moqClient.NewSigsClientMoqWithScheme(scheme, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{
					Name:      postgresProviderName + defaultCredSecSuffix,
					Namespace: testNs,
				},
				},
					&v1alpha1.Postgres{
						ObjectMeta: metav1.ObjectMeta{
							Name:      postgresProviderName,
							Namespace: testNs,
							Annotations: map[string]string{
								ResourceIdentifierAnnotation: testName,
							},
						},
					},
					&v1.Infrastructure{
						ObjectMeta: metav1.ObjectMeta{
							Name: testInfrastructureName,
						},
						Status: v1.InfrastructureStatus{
							InfrastructureName: testInfrastructureName,
						},
					},
				),
				Logger: logrus.NewEntry(logrus.StandardLogger()),
				CredentialManager: &CredentialManagerMock{
					ReconcileProviderCredentialsFunc: func(ctx context.Context, ns string) (*Credentials, error) {
						return nil, fmt.Errorf("failed to reconcile gcp postgres provider credentials for postgres instance")
					},
				},
			},
			args: args{
				ctx: context.TODO(),
				p: &v1alpha1.Postgres{
					ObjectMeta: metav1.ObjectMeta{
						Name:      postgresProviderName,
						Namespace: testNs,
						Annotations: map[string]string{
							ResourceIdentifierAnnotation: testName,
						},
						DeletionTimestamp: &metav1.Time{Time: now},
					},
				},
				sqladminService: gcpiface.GetMockSQLClient(nil),
			},
			want:    "failed to reconcile gcp postgres provider credentials for postgres instance gcp-cloudsql",
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pp := &PostgresProvider{
				Client:            tt.fields.Client,
				Logger:            tt.fields.Logger,
				CredentialManager: tt.fields.CredentialManager,
				ConfigManager:     tt.fields.ConfigManager,
			}
			got, err := pp.DeletePostgres(tt.args.ctx, tt.args.p)
			if (err != nil) != tt.wantErr {
				t.Errorf("DeletePostgres() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("DeletePostgres() got = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPostgresProvider_reconcileCloudSQLInstance(t *testing.T) {
	scheme, err := buildTestScheme()
	if err != nil {
		t.Fatal("failed to build scheme", err)
	}
	type fields struct {
		Client            client.Client
		Logger            *logrus.Entry
		CredentialManager CredentialManager
		ConfigManager     ConfigManager
	}
	type args struct {
		ctx                  context.Context
		p                    *v1alpha1.Postgres
		sqladminService      gcpiface.SQLAdminService
		cloudSQLCreateConfig *gcpiface.DatabaseInstance
		strategyConfig       *StrategyConfig
		maintenanceWindow    bool
	}
	tests := []struct {
		name    string
		fields  fields
		args    args
		want    types.StatusMessage
		wantErr bool
	}{
		{
			name: "error when retrieving cloudSQL instance",
			fields: fields{
				Client:            moqClient.NewSigsClientMoqWithScheme(scheme, buildTestPostgresSecret(), buildTestPostgres(), buildTestGcpInfrastructure(nil)),
				Logger:            logrus.NewEntry(logrus.StandardLogger()),
				CredentialManager: nil,
				ConfigManager:     nil,
			},
			args: args{
				ctx: context.TODO(),
				p:   buildTestPostgres(),
				sqladminService: gcpiface.GetMockSQLClient(func(sqlClient *gcpiface.MockSqlClient) {
					sqlClient.GetInstanceFn = func(ctx context.Context, s string, s2 string) (*sqladmin.DatabaseInstance, error) {
						return nil, errors.New("cannot retrieve sql instance from gcp")
					}
				}),
				cloudSQLCreateConfig: &gcpiface.DatabaseInstance{Name: gcpTestPostgresInstanceName},
				strategyConfig:       &StrategyConfig{ProjectID: gcpTestProjectId},
				maintenanceWindow:    false,
			},
			want:    "cannot retrieve sql instance from gcp",
			wantErr: true,
		},
		{
			name: "success building cloudSQL create strategy using defaults when settings object is not nil",
			fields: fields{
				Client: moqClient.NewSigsClientMoqWithScheme(scheme, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{
					Name:      postgresProviderName + defaultCredSecSuffix,
					Namespace: testNs,
				},
					Data: map[string][]byte{
						defaultPostgresUserKey:     []byte(testUser),
						defaultPostgresPasswordKey: []byte(testPassword),
					},
				}, buildTestPostgres(), buildTestGcpInfrastructure(nil)),
				Logger:            logrus.NewEntry(logrus.StandardLogger()),
				CredentialManager: NewCredentialMinterCredentialManager(nil),
				ConfigManager:     nil,
			},
			args: args{
				ctx: context.TODO(),
				p:   buildTestPostgres(),
				sqladminService: gcpiface.GetMockSQLClient(func(sqlClient *gcpiface.MockSqlClient) {
					sqlClient.InstancesListFn = func(s string) (*sqladmin.InstancesListResponse, error) {
						return &sqladmin.InstancesListResponse{
							Items: []*sqladmin.DatabaseInstance{},
						}, nil
					}
				}),
				cloudSQLCreateConfig: &gcpiface.DatabaseInstance{
					Settings: &gcpiface.Settings{
						BackupConfiguration: &gcpiface.BackupConfiguration{BackupRetentionSettings: &gcpiface.BackupRetentionSettings{}},
					},
				},
				strategyConfig:    &StrategyConfig{ProjectID: "sample-project-id"},
				maintenanceWindow: false,
			},
			want:    "started cloudSQL provision",
			wantErr: false,
		},
		{
			name: "success building cloudSQL create strategy using defaults when settings object is nil",
			fields: fields{
				Client: moqClient.NewSigsClientMoqWithScheme(scheme, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{
					Name:      postgresProviderName + defaultCredSecSuffix,
					Namespace: testNs,
				},
					Data: map[string][]byte{
						defaultPostgresUserKey:     []byte(testUser),
						defaultPostgresPasswordKey: []byte(testPassword),
					},
				}, buildTestPostgres(), buildTestGcpInfrastructure(nil)),
				Logger:            logrus.NewEntry(logrus.StandardLogger()),
				CredentialManager: NewCredentialMinterCredentialManager(nil),
				ConfigManager:     nil,
			},
			args: args{
				ctx: context.TODO(),
				p:   buildTestPostgres(),
				sqladminService: gcpiface.GetMockSQLClient(func(sqlClient *gcpiface.MockSqlClient) {
					sqlClient.InstancesListFn = func(s string) (*sqladmin.InstancesListResponse, error) {
						return &sqladmin.InstancesListResponse{
							Items: []*sqladmin.DatabaseInstance{},
						}, nil
					}
				}),
				cloudSQLCreateConfig: &gcpiface.DatabaseInstance{},
				strategyConfig:       &StrategyConfig{ProjectID: "sample-project-id"},
				maintenanceWindow:    false,
			},
			want:    "started cloudSQL provision",
			wantErr: false,
		},
		{
			name: "success building cloudSQL create strategy using defaults when backup config object is nil",
			fields: fields{
				Client: moqClient.NewSigsClientMoqWithScheme(scheme, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{
					Name:      postgresProviderName + defaultCredSecSuffix,
					Namespace: testNs,
				},
					Data: map[string][]byte{
						defaultPostgresUserKey:     []byte(testUser),
						defaultPostgresPasswordKey: []byte(testPassword),
					},
				}, buildTestPostgres(), buildTestGcpInfrastructure(nil)),
				Logger:            logrus.NewEntry(logrus.StandardLogger()),
				CredentialManager: NewCredentialMinterCredentialManager(nil),
				ConfigManager:     nil,
			},
			args: args{
				ctx: context.TODO(),
				p:   buildTestPostgres(),
				sqladminService: gcpiface.GetMockSQLClient(func(sqlClient *gcpiface.MockSqlClient) {
					sqlClient.InstancesListFn = func(s string) (*sqladmin.InstancesListResponse, error) {
						return &sqladmin.InstancesListResponse{
							Items: []*sqladmin.DatabaseInstance{},
						}, nil
					}
				}),
				cloudSQLCreateConfig: &gcpiface.DatabaseInstance{Settings: &gcpiface.Settings{}},
				strategyConfig:       &StrategyConfig{ProjectID: "sample-project-id"},
				maintenanceWindow:    false,
			},
			want:    "started cloudSQL provision",
			wantErr: false,
		},
		{
			name: "success finding instance",
			fields: fields{
				Client: moqClient.NewSigsClientMoqWithScheme(scheme, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{
					Name:      postgresProviderName + defaultCredSecSuffix,
					Namespace: testNs,
				},
					Data: map[string][]byte{
						defaultPostgresUserKey:     []byte(testUser),
						defaultPostgresPasswordKey: []byte(testPassword),
					},
				}, buildTestPostgres(), buildTestGcpInfrastructure(nil)),
				Logger:            logrus.NewEntry(logrus.StandardLogger()),
				CredentialManager: NewCredentialMinterCredentialManager(nil),
				ConfigManager:     nil,
			},
			args: args{
				ctx: context.TODO(),
				p:   buildTestPostgres(),
				sqladminService: gcpiface.GetMockSQLClient(func(sqlClient *gcpiface.MockSqlClient) {
					sqlClient.InstancesListFn = func(s string) (*sqladmin.InstancesListResponse, error) {
						return &sqladmin.InstancesListResponse{
							Items: []*sqladmin.DatabaseInstance{
								{
									Name:  gcpTestPostgresInstanceName,
									State: "RUNNABLE",
								},
							},
						}, nil
					}
				}),
				cloudSQLCreateConfig: &gcpiface.DatabaseInstance{
					Name: gcpTestPostgresInstanceName,
				},
				strategyConfig:    &StrategyConfig{ProjectID: "sample-project-id"},
				maintenanceWindow: false,
			},
			want:    "started cloudSQL provision",
			wantErr: false,
		},
		{
			name: "if found instance state is PENDING_CREATE return StatusMessage",
			fields: fields{
				Client: moqClient.NewSigsClientMoqWithScheme(scheme, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{
					Name:      postgresProviderName + defaultCredSecSuffix,
					Namespace: testNs,
				},
					Data: map[string][]byte{
						defaultPostgresUserKey:     []byte(testUser),
						defaultPostgresPasswordKey: []byte(testPassword),
					},
				}, buildTestPostgres(), buildTestGcpInfrastructure(nil)),
				Logger:            logrus.NewEntry(logrus.StandardLogger()),
				CredentialManager: NewCredentialMinterCredentialManager(nil),
				ConfigManager:     nil,
			},
			args: args{
				ctx: context.TODO(),
				p:   buildTestPostgres(),
				sqladminService: gcpiface.GetMockSQLClient(func(sqlClient *gcpiface.MockSqlClient) {
					sqlClient.GetInstanceFn = func(ctx context.Context, s string, s2 string) (*sqladmin.DatabaseInstance, error) {
						return &sqladmin.DatabaseInstance{
							Name:  gcpTestPostgresInstanceName,
							State: "PENDING_CREATE",
						}, nil
					}
				}),
				cloudSQLCreateConfig: &gcpiface.DatabaseInstance{
					Name: gcpTestPostgresInstanceName,
				},
				strategyConfig:    &StrategyConfig{ProjectID: "sample-project-id"},
				maintenanceWindow: false,
			},
			want:    "creation of " + gcpTestPostgresInstanceName + " cloudSQL instance in progress",
			wantErr: false,
		},
		{
			name: "error creating cloudSQL instance",
			fields: fields{
				Client: moqClient.NewSigsClientMoqWithScheme(scheme, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{
					Name:      postgresProviderName + defaultCredSecSuffix,
					Namespace: testNs,
				},
					Data: map[string][]byte{
						defaultPostgresUserKey:     []byte(testUser),
						defaultPostgresPasswordKey: []byte(testPassword),
					},
				}, buildTestPostgres(), buildTestGcpInfrastructure(nil)),
				Logger:            logrus.NewEntry(logrus.StandardLogger()),
				CredentialManager: NewCredentialMinterCredentialManager(nil),
				ConfigManager:     nil,
			},
			args: args{
				ctx: context.TODO(),
				p:   buildTestPostgres(),
				sqladminService: gcpiface.GetMockSQLClient(func(sqlClient *gcpiface.MockSqlClient) {
					sqlClient.CreateInstanceFn = func(ctx context.Context, s string, instance *sqladmin.DatabaseInstance) (*sqladmin.Operation, error) {
						return nil, errors.New("failed to create cloudSQL instance")
					}
				}),
				cloudSQLCreateConfig: &gcpiface.DatabaseInstance{
					Name: gcpTestPostgresInstanceName,
				},
				strategyConfig:    &StrategyConfig{ProjectID: "sample-project-id"},
				maintenanceWindow: false,
			},
			want:    "failed to create cloudSQL instance",
			wantErr: true,
		},
		{
			name: "failure to add annotation",
			fields: fields{
				Client: func() client.Client {
					mc := moqClient.NewSigsClientMoqWithScheme(scheme, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{
						Name:      postgresProviderName + defaultCredSecSuffix,
						Namespace: testNs,
					},
						Data: map[string][]byte{
							defaultPostgresUserKey:     []byte(testUser),
							defaultPostgresPasswordKey: []byte(testPassword),
						},
					}, buildTestPostgres(), buildTestGcpInfrastructure(nil))
					mc.UpdateFunc = func(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error {
						return errors.New("failed to add annotation")
					}
					return mc
				}(),
				Logger:            logrus.NewEntry(logrus.StandardLogger()),
				CredentialManager: NewCredentialMinterCredentialManager(nil),
				ConfigManager:     nil,
			},
			args: args{
				ctx: context.TODO(),
				p:   buildTestPostgres(),
				sqladminService: gcpiface.GetMockSQLClient(func(sqlClient *gcpiface.MockSqlClient) {
					sqlClient.InstancesListFn = func(s string) (*sqladmin.InstancesListResponse, error) {
						return &sqladmin.InstancesListResponse{
							Items: []*sqladmin.DatabaseInstance{
								{},
							},
						}, nil
					}
					sqlClient.CreateInstanceFn = func(ctx context.Context, s string, instance *sqladmin.DatabaseInstance) (*sqladmin.Operation, error) {
						return &sqladmin.Operation{}, nil
					}
				}),
				cloudSQLCreateConfig: &gcpiface.DatabaseInstance{},
				strategyConfig:       &StrategyConfig{ProjectID: "sample-project-id"},
				maintenanceWindow:    false,
			},
			want:    "failed to add annotation",
			wantErr: true,
		},
		{
			name: "error when modifying cloud sql instances",
			fields: fields{
				Client: moqClient.NewSigsClientMoqWithScheme(scheme, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{
					Name:      postgresProviderName + defaultCredSecSuffix,
					Namespace: testNs,
				},
					Data: map[string][]byte{
						defaultPostgresUserKey:     []byte(testUser),
						defaultPostgresPasswordKey: []byte(testPassword),
					},
				}, buildTestPostgres(), buildTestGcpInfrastructure(nil)),
				Logger:            logrus.NewEntry(logrus.StandardLogger()),
				CredentialManager: NewCredentialMinterCredentialManager(nil),
				ConfigManager:     nil,
			},
			args: args{
				ctx: context.TODO(),
				p:   buildTestPostgres(),
				sqladminService: gcpiface.GetMockSQLClient(func(sqlClient *gcpiface.MockSqlClient) {
					sqlClient.GetInstanceFn = func(ctx context.Context, s string, s2 string) (*sqladmin.DatabaseInstance, error) {
						return &sqladmin.DatabaseInstance{
							Name:            gcpTestPostgresInstanceName,
							State:           "RUNNABLE",
							DatabaseVersion: defaultGCPCLoudSQLDatabaseVersion,
							Settings: &sqladmin.Settings{
								BackupConfiguration: &sqladmin.BackupConfiguration{
									BackupRetentionSettings: &sqladmin.BackupRetentionSettings{
										RetentionUnit:   defaultBackupRetentionSettingsRetentionUnit,
										RetainedBackups: defaultBackupRetentionSettingsRetainedBackups,
									},
								},
							},
						}, nil
					}
					sqlClient.ModifyInstanceFn = func(ctx context.Context, s string, s2 string, instance *sqladmin.DatabaseInstance) (*sqladmin.Operation, error) {
						return nil, fmt.Errorf("generic error")
					}
				}),
				cloudSQLCreateConfig: &gcpiface.DatabaseInstance{
					Settings: &gcpiface.Settings{
						BackupConfiguration: &gcpiface.BackupConfiguration{BackupRetentionSettings: &gcpiface.BackupRetentionSettings{}},
					},
				},
				strategyConfig:    &StrategyConfig{ProjectID: "sample-project-id"},
				maintenanceWindow: true,
			},
			want:    "failed to modify cloudsql instance: " + gcpTestPostgresInstanceName,
			wantErr: true,
		},
		{
			name: "success when modifying cloud sql instances",
			fields: fields{
				Client: moqClient.NewSigsClientMoqWithScheme(scheme, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{
					Name:      postgresProviderName + defaultCredSecSuffix,
					Namespace: testNs,
				},
					Data: map[string][]byte{
						defaultPostgresUserKey:     []byte(testUser),
						defaultPostgresPasswordKey: []byte(testPassword),
					},
				}, buildTestPostgres(), buildTestGcpInfrastructure(nil)),
				Logger:            logrus.NewEntry(logrus.StandardLogger()),
				CredentialManager: NewCredentialMinterCredentialManager(nil),
				ConfigManager:     nil,
			},
			args: args{
				ctx: context.TODO(),
				p:   buildTestPostgres(),
				sqladminService: gcpiface.GetMockSQLClient(func(sqlClient *gcpiface.MockSqlClient) {
					sqlClient.GetInstanceFn = func(ctx context.Context, s string, s2 string) (*sqladmin.DatabaseInstance, error) {
						return &sqladmin.DatabaseInstance{
							Name:            gcpTestPostgresInstanceName,
							State:           "RUNNABLE",
							DatabaseVersion: defaultGCPCLoudSQLDatabaseVersion,
							IpAddresses: []*sqladmin.IpMapping{
								{
									IpAddress: "",
								},
							},
							Settings: &sqladmin.Settings{
								BackupConfiguration: &sqladmin.BackupConfiguration{
									Enabled:                    defaultDeleteProtectionEnabled,
									PointInTimeRecoveryEnabled: defaultPointInTimeRecoveryEnabled,
									BackupRetentionSettings: &sqladmin.BackupRetentionSettings{
										RetentionUnit:   defaultBackupRetentionSettingsRetentionUnit,
										RetainedBackups: defaultBackupRetentionSettingsRetainedBackups,
									},
								},
								DeletionProtectionEnabled: defaultDeleteProtectionEnabled,
								StorageAutoResize:         utils.Bool(defaultStorageAutoResize),
								IpConfiguration: &sqladmin.IpConfiguration{
									Ipv4Enabled: defaultIPConfigIPV4Enabled,
								},
							},
						}, nil
					}
					sqlClient.ModifyInstanceFn = func(ctx context.Context, s string, s2 string, instance *sqladmin.DatabaseInstance) (*sqladmin.Operation, error) {
						return nil, nil
					}
				}),
				cloudSQLCreateConfig: &gcpiface.DatabaseInstance{
					Settings: &gcpiface.Settings{
						DeletionProtectionEnabled: utils.Bool(false),
						StorageAutoResize:         utils.Bool(false),
						BackupConfiguration: &gcpiface.BackupConfiguration{
							Enabled:                    utils.Bool(false),
							PointInTimeRecoveryEnabled: utils.Bool(false),
							BackupRetentionSettings: &gcpiface.BackupRetentionSettings{
								RetentionUnit:   "RETENTION_UNIT_UNSPECIFIED",
								RetainedBackups: 20,
							}},
						IpConfiguration: &gcpiface.IpConfiguration{
							Ipv4Enabled: utils.Bool(false),
						},
					},
				},
				strategyConfig:    &StrategyConfig{ProjectID: "sample-project-id"},
				maintenanceWindow: true,
			},
			want:    "completed cloudSQL instance creation",
			wantErr: false,
		},
		{
			name: "error building update config for cloudsql instance when database version not present",
			fields: fields{
				Client: func() client.Client {
					mc := moqClient.NewSigsClientMoqWithScheme(scheme, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{
						Name:      postgresProviderName + defaultCredSecSuffix,
						Namespace: testNs,
					},
						Data: map[string][]byte{
							defaultPostgresUserKey:     []byte(testUser),
							defaultPostgresPasswordKey: []byte(testPassword),
						},
					}, buildTestPostgres(), buildTestGcpInfrastructure(nil))
					mc.UpdateFunc = func(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error {
						return fmt.Errorf("generic error")
					}
					return mc
				}(),
				Logger:            logrus.NewEntry(logrus.StandardLogger()),
				CredentialManager: NewCredentialMinterCredentialManager(nil),
				ConfigManager:     nil,
			},
			args: args{
				ctx: context.TODO(),
				p:   buildTestPostgres(),
				sqladminService: gcpiface.GetMockSQLClient(func(sqlClient *gcpiface.MockSqlClient) {
					sqlClient.GetInstanceFn = func(ctx context.Context, s string, s2 string) (*sqladmin.DatabaseInstance, error) {
						return &sqladmin.DatabaseInstance{
							Name:            gcpTestPostgresInstanceName,
							State:           "RUNNABLE",
							DatabaseVersion: "",
							IpAddresses: []*sqladmin.IpMapping{
								{
									IpAddress: "",
								},
							},
							Settings: &sqladmin.Settings{
								BackupConfiguration: &sqladmin.BackupConfiguration{
									BackupRetentionSettings: &sqladmin.BackupRetentionSettings{
										RetentionUnit:   defaultBackupRetentionSettingsRetentionUnit,
										RetainedBackups: defaultBackupRetentionSettingsRetainedBackups,
									},
								},
							},
						}, nil
					}
				}),
				cloudSQLCreateConfig: &gcpiface.DatabaseInstance{
					Settings: &gcpiface.Settings{
						BackupConfiguration: &gcpiface.BackupConfiguration{BackupRetentionSettings: &gcpiface.BackupRetentionSettings{}},
					},
				},
				strategyConfig:    &StrategyConfig{ProjectID: "sample-project-id"},
				maintenanceWindow: true,
			},
			want:    "error building update config for cloudsql instance",
			wantErr: true,
		},
		{
			name: "error when setting postgres maintenance window to false",
			fields: fields{
				Client: func() client.Client {
					mc := moqClient.NewSigsClientMoqWithScheme(scheme, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{
						Name:      postgresProviderName + defaultCredSecSuffix,
						Namespace: testNs,
					},
						Data: map[string][]byte{
							defaultPostgresUserKey:     []byte(testUser),
							defaultPostgresPasswordKey: []byte(testPassword),
						},
					}, &v1alpha1.Postgres{
						ObjectMeta: metav1.ObjectMeta{
							Name:      postgresProviderName,
							Namespace: testNs,
							Labels: map[string]string{
								"productName": "test_product",
							},
							ResourceVersion: "1000",
						},
						Spec: types.ResourceTypeSpec{
							MaintenanceWindow: true,
						},
					}, buildTestGcpInfrastructure(nil))
					mc.UpdateFunc = func(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error {
						return fmt.Errorf("generic error")
					}
					return mc
				}(),
				Logger:            logrus.NewEntry(logrus.StandardLogger()),
				CredentialManager: NewCredentialMinterCredentialManager(nil),
				ConfigManager:     nil,
			},
			args: args{
				ctx: context.TODO(),
				p:   buildTestPostgres(),
				sqladminService: gcpiface.GetMockSQLClient(func(sqlClient *gcpiface.MockSqlClient) {
					sqlClient.GetInstanceFn = func(ctx context.Context, s string, s2 string) (*sqladmin.DatabaseInstance, error) {
						return &sqladmin.DatabaseInstance{
							Name:            gcpTestPostgresInstanceName,
							State:           "RUNNABLE",
							DatabaseVersion: defaultGCPCLoudSQLDatabaseVersion,
							IpAddresses: []*sqladmin.IpMapping{
								{
									IpAddress: "",
								},
							},
							Settings: &sqladmin.Settings{
								BackupConfiguration: &sqladmin.BackupConfiguration{
									BackupRetentionSettings: &sqladmin.BackupRetentionSettings{
										RetentionUnit:   defaultBackupRetentionSettingsRetentionUnit,
										RetainedBackups: defaultBackupRetentionSettingsRetainedBackups,
									},
								},
							},
						}, nil
					}
				}),
				cloudSQLCreateConfig: &gcpiface.DatabaseInstance{
					Settings: &gcpiface.Settings{
						BackupConfiguration: &gcpiface.BackupConfiguration{BackupRetentionSettings: &gcpiface.BackupRetentionSettings{}},
					},
				},
				strategyConfig:    &StrategyConfig{ProjectID: "sample-project-id"},
				maintenanceWindow: true,
			},
			want:    "failed to set postgres maintenance window to false",
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pp := &PostgresProvider{
				Client:            tt.fields.Client,
				Logger:            tt.fields.Logger,
				CredentialManager: tt.fields.CredentialManager,
				ConfigManager:     tt.fields.ConfigManager,
			}
			_, got1, err := pp.reconcileCloudSQLInstance(tt.args.ctx, tt.args.p, tt.args.sqladminService, tt.args.cloudSQLCreateConfig, tt.args.strategyConfig, tt.args.maintenanceWindow)
			if (err != nil) != tt.wantErr {
				t.Errorf("reconcileCloudSQLInstance() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got1 != tt.want {
				t.Errorf("reconcileCloudSQLInstance() got1 = %v, want %v", got1, tt.want)
			}
		})
	}
}

func TestPostgresProvider_ReconcilePostgres(t *testing.T) {
	scheme, err := buildTestScheme()
	if err != nil {
		t.Fatal("failed to build scheme", err)
	}
	type fields struct {
		Client            client.Client
		Logger            *logrus.Entry
		CredentialManager CredentialManager
		ConfigManager     ConfigManager
	}
	type args struct {
		ctx context.Context
		p   *v1alpha1.Postgres
	}
	tests := []struct {
		name          string
		fields        fields
		args          args
		want          *providers.PostgresInstance
		statusMessage types.StatusMessage
		wantErr       bool
	}{
		{
			name: "failed to set finalizer",
			fields: fields{
				Client: func() client.Client {
					mc := moqClient.NewSigsClientMoqWithScheme(scheme)
					mc.CreateFunc = func(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
						return errors.New("failed to set finalizer")
					}
					return mc
				}(),
				Logger:            logrus.NewEntry(logrus.StandardLogger()),
				CredentialManager: &CredentialManagerMock{},
				ConfigManager:     &ConfigManagerMock{},
			},
			args: args{
				ctx: context.TODO(),
				p:   buildTestPostgres(),
			},
			want:          nil,
			statusMessage: "failed to set finalizer",
			wantErr:       true,
		},
		{
			name: "failed to retrieve postgres strategy config",
			fields: fields{
				Client:            moqClient.NewSigsClientMoqWithScheme(scheme, buildTestPostgres()),
				Logger:            logrus.NewEntry(logrus.StandardLogger()),
				CredentialManager: &CredentialManagerMock{},
				ConfigManager: &ConfigManagerMock{
					ReadStorageStrategyFunc: func(ctx context.Context, rt providers.ResourceType, tier string) (*StrategyConfig, error) {
						return &StrategyConfig{
							CreateStrategy: json.RawMessage("{ \"test\": \"test\" }"),
							DeleteStrategy: json.RawMessage("{ \"test\": \"test\" }"),
						}, nil
					},
				},
			},
			args: args{
				ctx: context.TODO(),
				p:   buildTestPostgres(),
			},
			want:          nil,
			statusMessage: "failed to retrieve postgres strategy config",
			wantErr:       true,
		},
		{
			name: "failed to reconcile gcp postgres provider credentials for postgres instance",
			fields: fields{
				Client: moqClient.NewSigsClientMoqWithScheme(scheme, buildTestPostgres(), buildTestGcpInfrastructure(nil)),
				Logger: logrus.NewEntry(logrus.StandardLogger()),
				CredentialManager: &CredentialManagerMock{
					ReconcileProviderCredentialsFunc: func(ctx context.Context, ns string) (*Credentials, error) {
						return nil, errors.New("generic error")
					},
				},
				ConfigManager: &ConfigManagerMock{
					ReadStorageStrategyFunc: func(ctx context.Context, rt providers.ResourceType, tier string) (*StrategyConfig, error) {
						return &StrategyConfig{
							CreateStrategy: json.RawMessage(`{}`),
							DeleteStrategy: json.RawMessage(`{}`),
						}, nil
					},
				},
			},
			args: args{
				ctx: context.TODO(),
				p:   buildTestPostgres(),
			},
			want:          nil,
			statusMessage: "failed to reconcile gcp postgres provider credentials for postgres instance gcp-cloudsql",
			wantErr:       true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pp := &PostgresProvider{
				Client:            tt.fields.Client,
				Logger:            tt.fields.Logger,
				CredentialManager: tt.fields.CredentialManager,
				ConfigManager:     tt.fields.ConfigManager,
			}
			got, statusMessage, err := pp.ReconcilePostgres(tt.args.ctx, tt.args.p)
			if (err != nil) != tt.wantErr {
				t.Errorf("ReconcilePostgres() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ReconcilePostgres() got = %v, want %v", got, tt.want)
			}
			if statusMessage != tt.statusMessage {
				t.Errorf("ReconcilePostgres() statusMessage = %v, want %v", statusMessage, tt.statusMessage)
			}
		})
	}
}

func TestPostgresProvider_getPostgresConfig(t *testing.T) {
	scheme, err := buildTestScheme()
	if err != nil {
		t.Fatal("failed to build scheme", err)
	}
	type fields struct {
		Client            client.Client
		Logger            *logrus.Entry
		CredentialManager CredentialManager
		ConfigManager     ConfigManager
	}
	type args struct {
		ctx context.Context
		pg  *v1alpha1.Postgres
	}
	tests := []struct {
		name                  string
		fields                fields
		args                  args
		createInstanceRequest *gcpiface.DatabaseInstance
		deleteInstanceRequest *sqladmin.DatabaseInstance
		strategyConfig        *StrategyConfig
		wantErr               bool
	}{
		{
			name: "success building create instance request",
			fields: fields{
				Client: func() client.Client {
					mc := moqClient.NewSigsClientMoqWithScheme(scheme, buildTestGcpInfrastructure(nil))
					mc.CreateFunc = func(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
						return nil
					}
					return mc
				}(),
				ConfigManager: &ConfigManagerMock{
					ReadStorageStrategyFunc: func(ctx context.Context, rt providers.ResourceType, tier string) (*StrategyConfig, error) {
						return &StrategyConfig{
							Region:         gcpTestRegion,
							ProjectID:      gcpTestProjectId,
							CreateStrategy: json.RawMessage(`{}`),
							DeleteStrategy: json.RawMessage(`{}`),
						}, nil
					},
				},
				Logger: logrus.NewEntry(logrus.StandardLogger()),
			},
			args: args{
				ctx: context.TODO(),
				pg: &v1alpha1.Postgres{
					TypeMeta: metav1.TypeMeta{},
					ObjectMeta: metav1.ObjectMeta{
						Name:      postgresProviderName,
						Namespace: testNs,
						Annotations: map[string]string{
							ResourceIdentifierAnnotation: testName,
						},
					},
					Spec: types.ResourceTypeSpec{
						Type: "postgres",
						Tier: "development",
					},
				},
			},
			createInstanceRequest: &gcpiface.DatabaseInstance{Name: gcpTestPostgresInstanceName},
			deleteInstanceRequest: &sqladmin.DatabaseInstance{Name: gcpTestPostgresInstanceName},
			strategyConfig:        buildTestStrategyConfig(),
			wantErr:               false,
		},
		{
			name: "failure building create instance request",
			fields: fields{
				Client: func() client.Client {
					mc := moqClient.NewSigsClientMoqWithScheme(scheme, buildTestGcpInfrastructure(nil))
					mc.CreateFunc = func(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
						return errors.New("failed to unmarshal gcp postgres create request")
					}
					return mc
				}(),
				ConfigManager: &ConfigManagerMock{
					ReadStorageStrategyFunc: func(ctx context.Context, rt providers.ResourceType, tier string) (*StrategyConfig, error) {
						return &StrategyConfig{
							Region:         gcpTestRegion,
							ProjectID:      gcpTestProjectId,
							CreateStrategy: nil,
							DeleteStrategy: nil,
						}, nil
					},
				},
			},
			args: args{
				pg: &v1alpha1.Postgres{
					ObjectMeta: metav1.ObjectMeta{
						Name:      postgresProviderName,
						Namespace: testNs,
						Annotations: map[string]string{
							ResourceIdentifierAnnotation: testName,
						},
					},
				},
			},
			createInstanceRequest: nil,
			deleteInstanceRequest: nil,
			strategyConfig:        nil,
			wantErr:               true,
		},
		{
			name: "success building delete instance request",
			fields: fields{
				Client: func() client.Client {
					mc := moqClient.NewSigsClientMoqWithScheme(scheme, buildTestGcpInfrastructure(nil))
					mc.CreateFunc = func(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
						return nil
					}
					mc.DeleteFunc = func(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error {
						return nil
					}
					return mc
				}(),
				ConfigManager: &ConfigManagerMock{
					ReadStorageStrategyFunc: func(ctx context.Context, rt providers.ResourceType, tier string) (*StrategyConfig, error) {
						return &StrategyConfig{
							Region:         gcpTestRegion,
							ProjectID:      gcpTestProjectId,
							CreateStrategy: json.RawMessage(`{}`),
							DeleteStrategy: json.RawMessage(`{}`),
						}, nil
					},
				},
				Logger: logrus.NewEntry(logrus.StandardLogger()),
			},
			args: args{
				ctx: context.TODO(),
				pg: &v1alpha1.Postgres{
					TypeMeta: metav1.TypeMeta{},
					ObjectMeta: metav1.ObjectMeta{
						Name:      postgresProviderName,
						Namespace: testNs,
						Annotations: map[string]string{
							ResourceIdentifierAnnotation: testName,
						},
					},
					Spec: types.ResourceTypeSpec{
						Type: "postgres",
						Tier: "development",
					},
				},
			},
			createInstanceRequest: &gcpiface.DatabaseInstance{Name: gcpTestPostgresInstanceName},
			deleteInstanceRequest: &sqladmin.DatabaseInstance{Name: gcpTestPostgresInstanceName},
			strategyConfig:        buildTestStrategyConfig(),
			wantErr:               false,
		},
		{
			name: "failure building delete instance request",
			fields: fields{
				Client: func() client.Client {
					mc := moqClient.NewSigsClientMoqWithScheme(scheme, buildTestGcpInfrastructure(nil))
					mc.DeleteFunc = func(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error {
						return errors.New("failed to unmarshal gcp postgres create request")
					}
					return mc
				}(),
				ConfigManager: &ConfigManagerMock{
					ReadStorageStrategyFunc: func(ctx context.Context, rt providers.ResourceType, tier string) (*StrategyConfig, error) {
						return &StrategyConfig{
							Region:         gcpTestRegion,
							ProjectID:      gcpTestProjectId,
							CreateStrategy: json.RawMessage(`{}`),
							DeleteStrategy: nil,
						}, nil
					},
				},
			},
			args: args{
				pg: &v1alpha1.Postgres{
					ObjectMeta: metav1.ObjectMeta{
						Name:      postgresProviderName,
						Namespace: testNs,
						Annotations: map[string]string{
							ResourceIdentifierAnnotation: testName,
						},
					},
				},
			},
			createInstanceRequest: nil,
			deleteInstanceRequest: nil,
			strategyConfig:        nil,
			wantErr:               true,
		},
		{
			name: "If strategyConfig.ProjectID is empty, log and set it to default project",
			fields: fields{
				Client: func() client.Client {
					mc := moqClient.NewSigsClientMoqWithScheme(scheme, buildTestGcpInfrastructure(nil))
					mc.CreateFunc = func(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
						return nil
					}
					return mc
				}(),
				ConfigManager: &ConfigManagerMock{
					ReadStorageStrategyFunc: func(ctx context.Context, rt providers.ResourceType, tier string) (*StrategyConfig, error) {
						return &StrategyConfig{
							Region:         gcpTestRegion,
							ProjectID:      "",
							CreateStrategy: json.RawMessage(`{}`),
							DeleteStrategy: json.RawMessage(`{}`),
						}, nil
					},
				},
				Logger: logrus.NewEntry(logrus.StandardLogger()),
			},
			args: args{
				ctx: context.TODO(),
				pg: &v1alpha1.Postgres{
					TypeMeta: metav1.TypeMeta{},
					ObjectMeta: metav1.ObjectMeta{
						Name:      postgresProviderName,
						Namespace: testNs,
						Annotations: map[string]string{
							ResourceIdentifierAnnotation: testName,
						},
					},
					Spec: types.ResourceTypeSpec{
						Type: "postgres",
						Tier: "development",
					},
				},
			},
			createInstanceRequest: &gcpiface.DatabaseInstance{Name: gcpTestPostgresInstanceName},
			deleteInstanceRequest: &sqladmin.DatabaseInstance{Name: gcpTestPostgresInstanceName},
			strategyConfig:        buildTestStrategyConfig(),
			wantErr:               false,
		},
		{
			name: "If strategyConfig.Region is empty, log and set it to default project",
			fields: fields{
				Client: func() client.Client {
					mc := moqClient.NewSigsClientMoqWithScheme(scheme, buildTestGcpInfrastructure(nil))
					mc.CreateFunc = func(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
						return nil
					}
					return mc
				}(),
				ConfigManager: &ConfigManagerMock{
					ReadStorageStrategyFunc: func(ctx context.Context, rt providers.ResourceType, tier string) (*StrategyConfig, error) {
						return &StrategyConfig{
							Region:         "",
							ProjectID:      gcpTestProjectId,
							CreateStrategy: json.RawMessage(`{}`),
							DeleteStrategy: json.RawMessage(`{}`),
						}, nil
					},
				},
				Logger: logrus.NewEntry(logrus.StandardLogger()),
			},
			args: args{
				ctx: context.TODO(),
				pg: &v1alpha1.Postgres{
					TypeMeta: metav1.TypeMeta{},
					ObjectMeta: metav1.ObjectMeta{
						Name:      postgresProviderName,
						Namespace: testNs,
						Annotations: map[string]string{
							ResourceIdentifierAnnotation: testName,
						},
					},
					Spec: types.ResourceTypeSpec{
						Type: "postgres",
						Tier: "development",
					},
				},
			},
			createInstanceRequest: &gcpiface.DatabaseInstance{Name: gcpTestPostgresInstanceName},
			deleteInstanceRequest: &sqladmin.DatabaseInstance{Name: gcpTestPostgresInstanceName},
			strategyConfig:        buildTestStrategyConfig(),
			wantErr:               false,
		},
		{
			name: "success building default postgres tags",
			fields: fields{
				Client: func() client.Client {
					mc := moqClient.NewSigsClientMoqWithScheme(scheme, buildTestGcpInfrastructure(nil))
					mc.CreateFunc = func(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
						return nil
					}
					return mc
				}(),
				ConfigManager: &ConfigManagerMock{
					ReadStorageStrategyFunc: func(ctx context.Context, rt providers.ResourceType, tier string) (*StrategyConfig, error) {
						return &StrategyConfig{
							Region:         gcpTestRegion,
							ProjectID:      gcpTestProjectId,
							CreateStrategy: json.RawMessage(`{"Settings": {"userLabels":{"integreatly-org_clusterid":"gcp-test-cluster","integreatly-org_resource-name":"testName","integreatly-org_resource-type":"","red-hat-managed":"true"}}}`),
							DeleteStrategy: json.RawMessage(`{}`),
						}, nil
					},
				},
				Logger: logrus.NewEntry(logrus.StandardLogger()),
			},
			args: args{
				ctx: context.TODO(),
				pg: &v1alpha1.Postgres{
					TypeMeta: metav1.TypeMeta{},
					ObjectMeta: metav1.ObjectMeta{
						Name:      postgresProviderName,
						Namespace: testNs,
						Annotations: map[string]string{
							ResourceIdentifierAnnotation: testName,
						},
					},
					Spec: types.ResourceTypeSpec{
						Type: "postgres",
						Tier: "development",
					},
				},
			},
			createInstanceRequest: &gcpiface.DatabaseInstance{
				Name: gcpTestPostgresInstanceName,
				Settings: &gcpiface.Settings{
					UserLabels: map[string]string{
						"integreatly-org_clusterid":     gcpTestClusterName,
						"integreatly-org_resource-name": testName,
						"integreatly-org_resource-type": "",
						"red-hat-managed":               "true",
					},
				},
			},
			deleteInstanceRequest: &sqladmin.DatabaseInstance{Name: gcpTestPostgresInstanceName},
			strategyConfig: &StrategyConfig{
				Region:         gcpTestRegion,
				ProjectID:      gcpTestProjectId,
				CreateStrategy: json.RawMessage(`{"Settings": {"userLabels":{"integreatly-org_clusterid":"gcp-test-cluster","integreatly-org_resource-name":"testName","integreatly-org_resource-type":"","red-hat-managed":"true"}}}`),
				DeleteStrategy: json.RawMessage(`{}`),
			},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &PostgresProvider{
				Client:            tt.fields.Client,
				Logger:            tt.fields.Logger,
				CredentialManager: tt.fields.CredentialManager,
				ConfigManager:     tt.fields.ConfigManager,
			}
			got, got1, got2, err := p.getPostgresConfig(tt.args.ctx, tt.args.pg)
			if (err != nil) != tt.wantErr {
				t.Errorf("getPostgresConfig() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.createInstanceRequest) {
				t.Errorf("getPostgresConfig() got = %v, want %v", got, tt.createInstanceRequest)
			}
			if !reflect.DeepEqual(got1, tt.deleteInstanceRequest) {
				t.Errorf("getPostgresConfig() got1 = %v, want %v", got1, tt.deleteInstanceRequest)
			}
			if !reflect.DeepEqual(got2, tt.strategyConfig) {
				t.Errorf("getPostgresConfig() got2 = %v, want %v", got2, tt.strategyConfig)
			}
		})
	}
}

func Test_formatGcpPostgresVersion(t *testing.T) {
	type args struct {
		gcpNewVersion      string
		gcpExistingVersion string
	}
	tests := []struct {
		name                      string
		args                      args
		wantSemverNewVersion      string
		wantSemverExistingVersion string
	}{
		{
			name: "success formatting gcp postgres version",
			args: args{
				gcpNewVersion:      "POSTGRES_14",
				gcpExistingVersion: defaultGCPCLoudSQLDatabaseVersion,
			},
			wantSemverNewVersion:      "14",
			wantSemverExistingVersion: "13",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotSemverNewVersion, gotSemverExistingVersion := formatGcpPostgresVersion(tt.args.gcpNewVersion, tt.args.gcpExistingVersion)
			if gotSemverNewVersion != tt.wantSemverNewVersion {
				t.Errorf("formatGcpPostgresVersion() gotSemverNewVersion = %v, want %v", gotSemverNewVersion, tt.wantSemverNewVersion)
			}
			if gotSemverExistingVersion != tt.wantSemverExistingVersion {
				t.Errorf("formatGcpPostgresVersion() gotSemverExistingVersion = %v, want %v", gotSemverExistingVersion, tt.wantSemverExistingVersion)
			}
		})
	}
}

func Test_convertDatabaseStruct(t *testing.T) {
	type args struct {
		cloudSQLCreateConfig *gcpiface.DatabaseInstance
	}
	tests := []struct {
		name    string
		args    args
		want    *sqladmin.DatabaseInstance
		wantErr bool
	}{
		{
			name: "success converting database struct",
			args: args{
				cloudSQLCreateConfig: &gcpiface.DatabaseInstance{
					ConnectionName:              testName,
					DatabaseVersion:             defaultGCPCLoudSQLDatabaseVersion,
					DiskEncryptionConfiguration: &sqladmin.DiskEncryptionConfiguration{},
					FailoverReplica: &gcpiface.DatabaseInstanceFailoverReplica{
						Available: utils.Bool(false),
						Name:      testName,
					},
					GceZone:      "test",
					InstanceType: "test",
					IpAddresses: []*sqladmin.IpMapping{
						{
							IpAddress: "",
						},
					},
					Kind:               "test",
					MaintenanceVersion: "test",
					MasterInstanceName: "test",
					MaxDiskSize:        100,
					Name:               testName,
					Project:            gcpTestProjectId,
					Region:             gcpTestRegion,
					ReplicaNames: []string{
						testName,
					},
					RootPassword:     testPassword,
					SecondaryGceZone: "test",
					SelfLink:         "test",
					ServerCaCert:     &sqladmin.SslCert{},
					Settings: &gcpiface.Settings{
						ActivationPolicy: "test",
						AvailabilityType: "test",
						BackupConfiguration: &gcpiface.BackupConfiguration{
							BackupRetentionSettings: &gcpiface.BackupRetentionSettings{
								RetentionUnit:   defaultBackupRetentionSettingsRetentionUnit,
								RetainedBackups: defaultBackupRetentionSettingsRetainedBackups,
							},
							BinaryLogEnabled:               utils.Bool(false),
							Enabled:                        utils.Bool(false),
							Kind:                           "test",
							Location:                       "test",
							PointInTimeRecoveryEnabled:     utils.Bool(defaultPointInTimeRecoveryEnabled),
							ReplicationLogArchivingEnabled: utils.Bool(false),
							StartTime:                      "test",
							TransactionLogRetentionDays:    1,
						},
						Collation:                   "test",
						ConnectorEnforcement:        "test",
						CrashSafeReplicationEnabled: utils.Bool(false),
						DataDiskSizeGb:              defaultDataDiskSizeGb,
						DataDiskType:                "test",
						DatabaseFlags:               []*sqladmin.DatabaseFlags{},
						DatabaseReplicationEnabled:  utils.Bool(false),
						DeletionProtectionEnabled:   utils.Bool(defaultDeleteProtectionEnabled),
						DenyMaintenancePeriods:      []*sqladmin.DenyMaintenancePeriod{},
						InsightsConfig:              &sqladmin.InsightsConfig{},
						IpConfiguration: &gcpiface.IpConfiguration{
							AllocatedIpRange:   "test",
							AuthorizedNetworks: []*sqladmin.AclEntry{},
							Ipv4Enabled:        utils.Bool(defaultIPConfigIPV4Enabled),
							PrivateNetwork:     "test",
							RequireSsl:         utils.Bool(true),
						},
						Kind:                     "test",
						LocationPreference:       &sqladmin.LocationPreference{},
						MaintenanceWindow:        &sqladmin.MaintenanceWindow{},
						PasswordValidationPolicy: &sqladmin.PasswordValidationPolicy{},
						PricingPlan:              "test",
						ReplicationType:          "test",
						SettingsVersion:          2,
						StorageAutoResize:        utils.Bool(defaultStorageAutoResize),
						StorageAutoResizeLimit:   defaultStorageAutoResizeLimit,
						Tier:                     "test",
						UserLabels: map[string]string{
							"integreatly-org_clusterid":     gcpTestClusterName,
							"integreatly-org_resource-name": testName,
							"integreatly-org_resource-type": "",
							"red-hat-managed":               "true",
						},
					},
				},
			},
			want: &sqladmin.DatabaseInstance{
				ConnectionName:              testName,
				DatabaseVersion:             defaultGCPCLoudSQLDatabaseVersion,
				DiskEncryptionConfiguration: &sqladmin.DiskEncryptionConfiguration{},
				FailoverReplica: &sqladmin.DatabaseInstanceFailoverReplica{
					Available: false,
					Name:      testName,
				},
				GceZone:      "test",
				InstanceType: "test",
				IpAddresses: []*sqladmin.IpMapping{
					{
						IpAddress: "",
					},
				},
				Kind:               "test",
				MaintenanceVersion: "test",
				MasterInstanceName: "test",
				MaxDiskSize:        100,
				Name:               testName,
				Project:            gcpTestProjectId,
				Region:             gcpTestRegion,
				ReplicaNames: []string{
					testName,
				},
				RootPassword: testPassword,
				SelfLink:     "test",
				ServerCaCert: &sqladmin.SslCert{},
				Settings: &sqladmin.Settings{
					ActivationPolicy: "test",
					AvailabilityType: "test",
					BackupConfiguration: &sqladmin.BackupConfiguration{
						BackupRetentionSettings: &sqladmin.BackupRetentionSettings{
							RetainedBackups: defaultBackupRetentionSettingsRetainedBackups,
							RetentionUnit:   defaultBackupRetentionSettingsRetentionUnit,
						},
						BinaryLogEnabled:               false,
						Enabled:                        false,
						Kind:                           "test",
						Location:                       "test",
						PointInTimeRecoveryEnabled:     true,
						ReplicationLogArchivingEnabled: false,
						StartTime:                      "test",
						TransactionLogRetentionDays:    1,
					},
					Collation:                   "test",
					ConnectorEnforcement:        "test",
					CrashSafeReplicationEnabled: false,
					DataDiskSizeGb:              defaultDataDiskSizeGb,
					DataDiskType:                "test",
					DatabaseFlags:               []*sqladmin.DatabaseFlags{},
					DatabaseReplicationEnabled:  false,
					DeletionProtectionEnabled:   defaultDeleteProtectionEnabled,
					DenyMaintenancePeriods:      []*sqladmin.DenyMaintenancePeriod{},
					InsightsConfig:              &sqladmin.InsightsConfig{},
					IpConfiguration: &sqladmin.IpConfiguration{
						AllocatedIpRange:   "test",
						AuthorizedNetworks: []*sqladmin.AclEntry{},
						Ipv4Enabled:        defaultIPConfigIPV4Enabled,
						PrivateNetwork:     "test",
						RequireSsl:         true,
					},
					Kind:                     "test",
					LocationPreference:       &sqladmin.LocationPreference{},
					MaintenanceWindow:        &sqladmin.MaintenanceWindow{},
					PasswordValidationPolicy: &sqladmin.PasswordValidationPolicy{},
					PricingPlan:              "test",
					ReplicationType:          "test",
					SettingsVersion:          2,
					StorageAutoResize:        utils.Bool(defaultStorageAutoResize),
					StorageAutoResizeLimit:   defaultStorageAutoResizeLimit,
					Tier:                     "test",
					UserLabels: map[string]string{
						"integreatly-org_clusterid":     gcpTestClusterName,
						"integreatly-org_resource-name": testName,
						"integreatly-org_resource-type": "",
						"red-hat-managed":               "true",
					},
				},
			},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := convertDatabaseStruct(tt.args.cloudSQLCreateConfig)
			if (err != nil) != tt.wantErr {
				t.Errorf("convertDatabaseStruct() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("convertDatabaseStruct() got = %v, want %v", got, tt.want)
			}
		})
	}
}
