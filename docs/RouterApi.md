# \RouterApi

All URIs are relative to *https://192.168.8.1*

Method | HTTP request | Description
------------- | ------------- | -------------
[**GetRouterHello**](RouterApi.md#GetRouterHello) | **Get** /router/hello | Check router is connected and configured. No login permission required.
[**RouterLogin**](RouterApi.md#RouterLogin) | **Post** /router/login | Log in to the router. No login permission required.



## GetRouterHello

> RouterHello GetRouterHello(ctx).Execute()

Check router is connected and configured. No login permission required.

### Example

```go
package main

import (
    "context"
    "fmt"
    "os"
    openapiclient "./openapi"
)

func main() {

    configuration := openapiclient.NewConfiguration()
    apiClient := openapiclient.NewAPIClient(configuration)
    resp, r, err := apiClient.RouterApi.GetRouterHello(context.Background()).Execute()
    if err != nil {
        fmt.Fprintf(os.Stderr, "Error when calling `RouterApi.GetRouterHello``: %v\n", err)
        fmt.Fprintf(os.Stderr, "Full HTTP response: %v\n", r)
    }
    // response from `GetRouterHello`: RouterHello
    fmt.Fprintf(os.Stdout, "Response from `RouterApi.GetRouterHello`: %v\n", resp)
}
```

### Path Parameters

This endpoint does not need any parameter.

### Other Parameters

Other parameters are passed through a pointer to a apiGetRouterHelloRequest struct via the builder pattern


### Return type

[**RouterHello**](RouterHello.md)

### Authorization

No authorization required

### HTTP request headers

- **Content-Type**: Not defined
- **Accept**: application/json

[[Back to top]](#) [[Back to API list]](../README.md#documentation-for-api-endpoints)
[[Back to Model list]](../README.md#documentation-for-models)
[[Back to README]](../README.md)


## RouterLogin

> RouterLoginResponse RouterLogin(ctx).RouterLogin(routerLogin).Execute()

Log in to the router. No login permission required.

### Example

```go
package main

import (
    "context"
    "fmt"
    "os"
    openapiclient "./openapi"
)

func main() {
    routerLogin := *openapiclient.NewRouterLogin() // RouterLogin |  (optional)

    configuration := openapiclient.NewConfiguration()
    apiClient := openapiclient.NewAPIClient(configuration)
    resp, r, err := apiClient.RouterApi.RouterLogin(context.Background()).RouterLogin(routerLogin).Execute()
    if err != nil {
        fmt.Fprintf(os.Stderr, "Error when calling `RouterApi.RouterLogin``: %v\n", err)
        fmt.Fprintf(os.Stderr, "Full HTTP response: %v\n", r)
    }
    // response from `RouterLogin`: RouterLoginResponse
    fmt.Fprintf(os.Stdout, "Response from `RouterApi.RouterLogin`: %v\n", resp)
}
```

### Path Parameters



### Other Parameters

Other parameters are passed through a pointer to a apiRouterLoginRequest struct via the builder pattern


Name | Type | Description  | Notes
------------- | ------------- | ------------- | -------------
 **routerLogin** | [**RouterLogin**](RouterLogin.md) |  | 

### Return type

[**RouterLoginResponse**](RouterLoginResponse.md)

### Authorization

No authorization required

### HTTP request headers

- **Content-Type**: application/json, multipart/form-data
- **Accept**: application/json

[[Back to top]](#) [[Back to API list]](../README.md#documentation-for-api-endpoints)
[[Back to Model list]](../README.md#documentation-for-models)
[[Back to README]](../README.md)

