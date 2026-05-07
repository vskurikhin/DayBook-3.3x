# \JsonRecordControllerApi

All URIs are relative to *http://localhost:8081*

Method | HTTP request | Description
------------- | ------------- | -------------
[**create_json_record**](JsonRecordControllerApi.md#create_json_record) | **POST** /core/api/v2/json-record | 
[**delete_json_record**](JsonRecordControllerApi.md#delete_json_record) | **DELETE** /core/api/v2/json-record/{id} | 
[**read_json_record**](JsonRecordControllerApi.md#read_json_record) | **GET** /core/api/v2/json-record/{id} | 
[**update_json_record**](JsonRecordControllerApi.md#update_json_record) | **PUT** /core/api/v2/json-record | 



## create_json_record

> models::ResourceJsonRecord create_json_record(new_json_record)


### Parameters


Name | Type | Description  | Required | Notes
------------- | ------------- | ------------- | ------------- | -------------
**new_json_record** | [**NewJsonRecord**](NewJsonRecord.md) |  | [required] |

### Return type

[**models::ResourceJsonRecord**](ResourceJsonRecord.md)

### Authorization

No authorization required

### HTTP request headers

- **Content-Type**: application/json
- **Accept**: */*

[[Back to top]](#) [[Back to API list]](../README.md#documentation-for-api-endpoints) [[Back to Model list]](../README.md#documentation-for-models) [[Back to README]](../README.md)


## delete_json_record

> delete_json_record(id)


### Parameters


Name | Type | Description  | Required | Notes
------------- | ------------- | ------------- | ------------- | -------------
**id** | **uuid::Uuid** |  | [required] |

### Return type

 (empty response body)

### Authorization

No authorization required

### HTTP request headers

- **Content-Type**: Not defined
- **Accept**: */*

[[Back to top]](#) [[Back to API list]](../README.md#documentation-for-api-endpoints) [[Back to Model list]](../README.md#documentation-for-models) [[Back to README]](../README.md)


## read_json_record

> models::ResourceJsonRecord read_json_record(id)


### Parameters


Name | Type | Description  | Required | Notes
------------- | ------------- | ------------- | ------------- | -------------
**id** | **uuid::Uuid** |  | [required] |

### Return type

[**models::ResourceJsonRecord**](ResourceJsonRecord.md)

### Authorization

No authorization required

### HTTP request headers

- **Content-Type**: Not defined
- **Accept**: */*

[[Back to top]](#) [[Back to API list]](../README.md#documentation-for-api-endpoints) [[Back to Model list]](../README.md#documentation-for-models) [[Back to README]](../README.md)


## update_json_record

> models::ResourceJsonRecord update_json_record(update_json_record)


### Parameters


Name | Type | Description  | Required | Notes
------------- | ------------- | ------------- | ------------- | -------------
**update_json_record** | [**UpdateJsonRecord**](UpdateJsonRecord.md) |  | [required] |

### Return type

[**models::ResourceJsonRecord**](ResourceJsonRecord.md)

### Authorization

No authorization required

### HTTP request headers

- **Content-Type**: application/json
- **Accept**: */*

[[Back to top]](#) [[Back to API list]](../README.md#documentation-for-api-endpoints) [[Back to Model list]](../README.md#documentation-for-models) [[Back to README]](../README.md)

