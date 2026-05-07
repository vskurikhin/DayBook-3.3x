# core.HelloControllerApi

All URIs are relative to *http://localhost:8081*

Method | HTTP request | Description
------------- | ------------- | -------------
[**hello**](HelloControllerApi.md#hello) | **GET** /core/api/v2/hello | 


# **hello**
> str hello()

### Example


```python
import core
from core.rest import ApiException
from pprint import pprint

# Defining the host is optional and defaults to http://localhost:8081
# See configuration.py for a list of all supported configuration parameters.
configuration = core.Configuration(
    host = "http://localhost:8081"
)


# Enter a context with an instance of the API client
with core.ApiClient(configuration) as api_client:
    # Create an instance of the API class
    api_instance = core.HelloControllerApi(api_client)

    try:
        api_response = api_instance.hello()
        print("The response of HelloControllerApi->hello:\n")
        pprint(api_response)
    except Exception as e:
        print("Exception when calling HelloControllerApi->hello: %s\n" % e)
```



### Parameters

This endpoint does not need any parameter.

### Return type

**str**

### Authorization

No authorization required

### HTTP request headers

 - **Content-Type**: Not defined
 - **Accept**: */*

### HTTP response details

| Status code | Description | Response headers |
|-------------|-------------|------------------|
**400** | Bad Request |  -  |
**500** | Internal Server Error |  -  |
**200** | OK |  -  |

[[Back to top]](#) [[Back to API list]](../README.md#documentation-for-api-endpoints) [[Back to Model list]](../README.md#documentation-for-models) [[Back to README]](../README.md)

