# HelloControllerApi

All URIs are relative to *http://localhost:8081*

| Method | HTTP request | Description |
|------------- | ------------- | -------------|
| [**hello**](HelloControllerApi.md#hello) | **GET** /core/api/v2/hello |  |


<a id="hello"></a>
# **hello**
> String hello()



### Example
```java
// Import classes:
import su.svn.lib.core.ApiClient;
import su.svn.lib.core.ApiException;
import su.svn.lib.core.Configuration;
import su.svn.lib.core.models.*;
import su.svn.lib.core.api.HelloControllerApi;

public class Example {
  public static void main(String[] args) {
    ApiClient defaultClient = Configuration.getDefaultApiClient();
    defaultClient.setBasePath("http://localhost:8081");

    HelloControllerApi apiInstance = new HelloControllerApi(defaultClient);
    try {
      String result = apiInstance.hello();
      System.out.println(result);
    } catch (ApiException e) {
      System.err.println("Exception when calling HelloControllerApi#hello");
      System.err.println("Status code: " + e.getCode());
      System.err.println("Reason: " + e.getResponseBody());
      System.err.println("Response headers: " + e.getResponseHeaders());
      e.printStackTrace();
    }
  }
}
```

### Parameters
This endpoint does not need any parameter.

### Return type

**String**

### Authorization

No authorization required

### HTTP request headers

 - **Content-Type**: Not defined
 - **Accept**: */*

### HTTP response details
| Status code | Description | Response headers |
|-------------|-------------|------------------|
| **400** | Bad Request |  -  |
| **404** | Not Found |  -  |
| **500** | Internal Server Error |  -  |
| **200** | OK |  -  |

