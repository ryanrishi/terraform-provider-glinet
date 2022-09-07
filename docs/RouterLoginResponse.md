# RouterLoginResponse

## Properties

Name | Type | Description | Notes
------------ | ------------- | ------------- | -------------
**Code** | Pointer to **float32** | Code:   * &#x60;0&#x60; success   * &#x60;-1&#x60; invalid user, permission denied or not logged in   * &#x60;-4&#x60; invalid parameter, value or format   * &#x60;-5&#x60; no parameter found   * &#x60;-6&#x60; time out   * &#x60;-9&#x60; wrong password  | [optional] 
**Token** | Pointer to **string** |  | [optional] 

## Methods

### NewRouterLoginResponse

`func NewRouterLoginResponse() *RouterLoginResponse`

NewRouterLoginResponse instantiates a new RouterLoginResponse object
This constructor will assign default values to properties that have it defined,
and makes sure properties required by API are set, but the set of arguments
will change when the set of required properties is changed

### NewRouterLoginResponseWithDefaults

`func NewRouterLoginResponseWithDefaults() *RouterLoginResponse`

NewRouterLoginResponseWithDefaults instantiates a new RouterLoginResponse object
This constructor will only assign default values to properties that have it defined,
but it doesn't guarantee that properties required by API are set

### GetCode

`func (o *RouterLoginResponse) GetCode() float32`

GetCode returns the Code field if non-nil, zero value otherwise.

### GetCodeOk

`func (o *RouterLoginResponse) GetCodeOk() (*float32, bool)`

GetCodeOk returns a tuple with the Code field if it's non-nil, zero value otherwise
and a boolean to check if the value has been set.

### SetCode

`func (o *RouterLoginResponse) SetCode(v float32)`

SetCode sets Code field to given value.

### HasCode

`func (o *RouterLoginResponse) HasCode() bool`

HasCode returns a boolean if a field has been set.

### GetToken

`func (o *RouterLoginResponse) GetToken() string`

GetToken returns the Token field if non-nil, zero value otherwise.

### GetTokenOk

`func (o *RouterLoginResponse) GetTokenOk() (*string, bool)`

GetTokenOk returns a tuple with the Token field if it's non-nil, zero value otherwise
and a boolean to check if the value has been set.

### SetToken

`func (o *RouterLoginResponse) SetToken(v string)`

SetToken sets Token field to given value.

### HasToken

`func (o *RouterLoginResponse) HasToken() bool`

HasToken returns a boolean if a field has been set.


[[Back to Model list]](../README.md#documentation-for-models) [[Back to API list]](../README.md#documentation-for-api-endpoints) [[Back to README]](../README.md)


