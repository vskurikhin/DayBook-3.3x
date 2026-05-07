# core.RecordViewControllerApi

All URIs are relative to *http://localhost:8081*

Method | HTTP request | Description
------------- | ------------- | -------------
[**get_all_records**](RecordViewControllerApi.md#get_all_records) | **GET** /core/api/v2/records-view | 


# **get_all_records**
> PagedModelEntityModelResourceRecordView get_all_records(filter, pageable)

### Example


```python
import core
from core.models.pageable import Pageable
from core.models.paged_model_entity_model_resource_record_view import PagedModelEntityModelResourceRecordView
from core.models.resource_record_view_filter import ResourceRecordViewFilter
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
    api_instance = core.RecordViewControllerApi(api_client)
    filter = core.ResourceRecordViewFilter() # ResourceRecordViewFilter | 
    pageable = core.Pageable() # Pageable | 

    try:
        api_response = api_instance.get_all_records(filter, pageable)
        print("The response of RecordViewControllerApi->get_all_records:\n")
        pprint(api_response)
    except Exception as e:
        print("Exception when calling RecordViewControllerApi->get_all_records: %s\n" % e)
```



### Parameters


Name | Type | Description  | Notes
------------- | ------------- | ------------- | -------------
 **filter** | [**ResourceRecordViewFilter**](.md)|  | 
 **pageable** | [**Pageable**](.md)|  | 

### Return type

[**PagedModelEntityModelResourceRecordView**](PagedModelEntityModelResourceRecordView.md)

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

