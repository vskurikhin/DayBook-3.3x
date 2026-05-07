# core.JsonRecordControllerApi

All URIs are relative to *http://localhost:8081*

Method | HTTP request | Description
------------- | ------------- | -------------
[**create_json_record**](JsonRecordControllerApi.md#create_json_record) | **POST** /core/api/v2/json-record | 
[**delete_json_record**](JsonRecordControllerApi.md#delete_json_record) | **DELETE** /core/api/v2/json-record/{id} | 
[**read_json_record**](JsonRecordControllerApi.md#read_json_record) | **GET** /core/api/v2/json-record/{id} | 
[**update_json_record**](JsonRecordControllerApi.md#update_json_record) | **PUT** /core/api/v2/json-record | 


# **create_json_record**
> ResourceJsonRecord create_json_record(new_json_record)

### Example


```python
import core
from core.models.new_json_record import NewJsonRecord
from core.models.resource_json_record import ResourceJsonRecord
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
    api_instance = core.JsonRecordControllerApi(api_client)
    new_json_record = core.NewJsonRecord() # NewJsonRecord | 

    try:
        api_response = api_instance.create_json_record(new_json_record)
        print("The response of JsonRecordControllerApi->create_json_record:\n")
        pprint(api_response)
    except Exception as e:
        print("Exception when calling JsonRecordControllerApi->create_json_record: %s\n" % e)
```



### Parameters


Name | Type | Description  | Notes
------------- | ------------- | ------------- | -------------
 **new_json_record** | [**NewJsonRecord**](NewJsonRecord.md)|  | 

### Return type

[**ResourceJsonRecord**](ResourceJsonRecord.md)

### Authorization

No authorization required

### HTTP request headers

 - **Content-Type**: application/json
 - **Accept**: */*

### HTTP response details

| Status code | Description | Response headers |
|-------------|-------------|------------------|
**400** | Bad Request |  -  |
**500** | Internal Server Error |  -  |
**200** | OK |  -  |

[[Back to top]](#) [[Back to API list]](../README.md#documentation-for-api-endpoints) [[Back to Model list]](../README.md#documentation-for-models) [[Back to README]](../README.md)

# **delete_json_record**
> delete_json_record(id)

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
    api_instance = core.JsonRecordControllerApi(api_client)
    id = UUID('38400000-8cf0-11bd-b23e-10b96e4ef00d') # UUID | 

    try:
        api_instance.delete_json_record(id)
    except Exception as e:
        print("Exception when calling JsonRecordControllerApi->delete_json_record: %s\n" % e)
```



### Parameters


Name | Type | Description  | Notes
------------- | ------------- | ------------- | -------------
 **id** | **UUID**|  | 

### Return type

void (empty response body)

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

# **read_json_record**
> ResourceJsonRecord read_json_record(id)

### Example


```python
import core
from core.models.resource_json_record import ResourceJsonRecord
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
    api_instance = core.JsonRecordControllerApi(api_client)
    id = UUID('38400000-8cf0-11bd-b23e-10b96e4ef00d') # UUID | 

    try:
        api_response = api_instance.read_json_record(id)
        print("The response of JsonRecordControllerApi->read_json_record:\n")
        pprint(api_response)
    except Exception as e:
        print("Exception when calling JsonRecordControllerApi->read_json_record: %s\n" % e)
```



### Parameters


Name | Type | Description  | Notes
------------- | ------------- | ------------- | -------------
 **id** | **UUID**|  | 

### Return type

[**ResourceJsonRecord**](ResourceJsonRecord.md)

### Authorization

No authorization required

### HTTP request headers

 - **Content-Type**: Not defined
 - **Accept**: */*

### HTTP response details

| Status code | Description | Response headers |
|-------------|-------------|------------------|
**404** | Not Found |  -  |
**400** | Bad Request |  -  |
**500** | Internal Server Error |  -  |
**200** | OK |  -  |

[[Back to top]](#) [[Back to API list]](../README.md#documentation-for-api-endpoints) [[Back to Model list]](../README.md#documentation-for-models) [[Back to README]](../README.md)

# **update_json_record**
> ResourceJsonRecord update_json_record(update_json_record)

### Example


```python
import core
from core.models.resource_json_record import ResourceJsonRecord
from core.models.update_json_record import UpdateJsonRecord
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
    api_instance = core.JsonRecordControllerApi(api_client)
    update_json_record = core.UpdateJsonRecord() # UpdateJsonRecord | 

    try:
        api_response = api_instance.update_json_record(update_json_record)
        print("The response of JsonRecordControllerApi->update_json_record:\n")
        pprint(api_response)
    except Exception as e:
        print("Exception when calling JsonRecordControllerApi->update_json_record: %s\n" % e)
```



### Parameters


Name | Type | Description  | Notes
------------- | ------------- | ------------- | -------------
 **update_json_record** | [**UpdateJsonRecord**](UpdateJsonRecord.md)|  | 

### Return type

[**ResourceJsonRecord**](ResourceJsonRecord.md)

### Authorization

No authorization required

### HTTP request headers

 - **Content-Type**: application/json
 - **Accept**: */*

### HTTP response details

| Status code | Description | Response headers |
|-------------|-------------|------------------|
**400** | Bad Request |  -  |
**500** | Internal Server Error |  -  |
**200** | OK |  -  |

[[Back to top]](#) [[Back to API list]](../README.md#documentation-for-api-endpoints) [[Back to Model list]](../README.md#documentation-for-models) [[Back to README]](../README.md)

