# RecordViewControllerApi

All URIs are relative to *http://localhost:8081*

| Method | HTTP request | Description |
|------------- | ------------- | -------------|
| [**getAllRecords**](RecordViewControllerApi.md#getAllRecords) | **GET** /core/api/v2/json-records |  |


<a id="getAllRecords"></a>
# **getAllRecords**
> PageResourceRecordView getAllRecords(filter, pageable)



### Example
```java
// Import classes:
import su.svn.ApiClient;
import su.svn.ApiException;
import su.svn.Configuration;
import su.svn.models.*;
import su.svn.lib.RecordViewControllerApi;

public class Example {
  public static void main(String[] args) {
    ApiClient defaultClient = Configuration.getDefaultApiClient();
    defaultClient.setBasePath("http://localhost:8081");

    RecordViewControllerApi apiInstance = new RecordViewControllerApi(defaultClient);
    ResourceRecordViewFilter filter = new ResourceRecordViewFilter(); // ResourceRecordViewFilter | 
    Pageable pageable = new Pageable(); // Pageable | 
    try {
      PageResourceRecordView result = apiInstance.getAllRecords(filter, pageable);
      System.out.println(result);
    } catch (ApiException e) {
      System.err.println("Exception when calling RecordViewControllerApi#getAllRecords");
      System.err.println("Status code: " + e.getCode());
      System.err.println("Reason: " + e.getResponseBody());
      System.err.println("Response headers: " + e.getResponseHeaders());
      e.printStackTrace();
    }
  }
}
```

### Parameters

| Name | Type | Description  | Notes |
|------------- | ------------- | ------------- | -------------|
| **filter** | [**ResourceRecordViewFilter**](.md)|  | |
| **pageable** | [**Pageable**](.md)|  | |

### Return type

[**PageResourceRecordView**](PageResourceRecordView.md)

### Authorization

No authorization required

### HTTP request headers

 - **Content-Type**: Not defined
 - **Accept**: */*

### HTTP response details
| Status code | Description | Response headers |
|-------------|-------------|------------------|
| **500** | Internal Server Error |  -  |
| **400** | Bad Request |  -  |
| **200** | OK |  -  |

