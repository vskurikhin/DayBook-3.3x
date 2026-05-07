# \RecordViewControllerApi

All URIs are relative to *http://localhost:8081*

Method | HTTP request | Description
------------- | ------------- | -------------
[**get_all_records**](RecordViewControllerApi.md#get_all_records) | **GET** /core/api/v2/records-view | 



## get_all_records

> models::PagedModelEntityModelResourceRecordView get_all_records(filter, pageable)


### Parameters


Name | Type | Description  | Required | Notes
------------- | ------------- | ------------- | ------------- | -------------
**filter** | [**ResourceRecordViewFilter**](ResourceRecordViewFilter.md) |  | [required] |
**pageable** | [**Pageable**](Pageable.md) |  | [required] |

### Return type

[**models::PagedModelEntityModelResourceRecordView**](PagedModelEntityModelResourceRecordView.md)

### Authorization

No authorization required

### HTTP request headers

- **Content-Type**: Not defined
- **Accept**: */*

[[Back to top]](#) [[Back to API list]](../README.md#documentation-for-api-endpoints) [[Back to Model list]](../README.md#documentation-for-models) [[Back to README]](../README.md)

