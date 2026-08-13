import React, { useCallback, useEffect, useState } from 'react';
import { Button, Card, Col, Form, Input, InputNumber, Row, message } from 'antd';
import { SaveOutlined } from '@ant-design/icons';
import { apiService } from '../../services/api';
import { mapApiError } from '../../utils/mapApiError';

interface BusinessSettingsPayload {
  upload_max_size_bytes: number;
  max_document_pages: number;
  upload_token_ttl_seconds: number;
  download_token_ttl_seconds: number;
  allowed_extensions: string[];
  max_contacts_per_node: number;
}

interface BusinessSettingsFormValues {
  upload_size_mb: number;
  max_document_pages: number;
  upload_token_ttl_seconds: number;
  download_token_ttl_seconds: number;
  allowed_extensions: string;
  max_contacts_per_node: number;
}

const bytesToMegabytes = (bytes: number): number => Number((bytes / (1024 * 1024)).toFixed(2));
const megabytesToBytes = (megabytes: number): number => Math.round(megabytes * 1024 * 1024);

const parseExtensions = (value: string): string[] =>
  value
    .split(',')
    .map((extension) => extension.trim())
    .filter(Boolean);

const BusinessSettings: React.FC = () => {
  const [form] = Form.useForm<BusinessSettingsFormValues>();
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);

  const applySettings = useCallback((settings: BusinessSettingsPayload) => {
    form.setFieldsValue({
      upload_size_mb: bytesToMegabytes(settings.upload_max_size_bytes),
      max_document_pages: settings.max_document_pages,
      upload_token_ttl_seconds: settings.upload_token_ttl_seconds,
      download_token_ttl_seconds: settings.download_token_ttl_seconds,
      allowed_extensions: settings.allowed_extensions.join(', '),
      max_contacts_per_node: settings.max_contacts_per_node ?? 5,
    });
  }, [form]);

  const loadSettings = useCallback(async () => {
    setLoading(true);
    try {
      const result = await apiService.get<BusinessSettingsPayload>('/admin/business-settings');
      if (result.code === 200 && result.data) {
        applySettings(result.data);
      } else {
        message.error(mapApiError(result, '加载业务配置失败'));
      }
    } catch (error) {
      message.error(mapApiError(error, '网络错误'));
    } finally {
      setLoading(false);
    }
  }, [applySettings]);

  useEffect(() => {
    void loadSettings();
  }, [loadSettings]);

  const saveSettings = async (values: BusinessSettingsFormValues) => {
    setSaving(true);
    try {
      const payload: BusinessSettingsPayload = {
        upload_max_size_bytes: megabytesToBytes(values.upload_size_mb),
        max_document_pages: values.max_document_pages,
        upload_token_ttl_seconds: values.upload_token_ttl_seconds,
        download_token_ttl_seconds: values.download_token_ttl_seconds,
        allowed_extensions: parseExtensions(values.allowed_extensions),
        max_contacts_per_node: values.max_contacts_per_node,
      };

      const result = await apiService.put<BusinessSettingsPayload>('/admin/business-settings', payload);
      if (result.code === 200 && result.data) {
        applySettings(result.data);
        message.success('业务配置已更新');
      } else {
        message.error(mapApiError(result, '保存业务配置失败'));
      }
    } catch (error) {
      message.error(mapApiError(error, '网络错误'));
    } finally {
      setSaving(false);
    }
  };

  return (
    <div>
      <Card loading={loading}>
        <Form
          form={form}
          layout="vertical"
          onFinish={saveSettings}
          initialValues={{
            upload_size_mb: 10,
            max_document_pages: 5,
            upload_token_ttl_seconds: 180,
            download_token_ttl_seconds: 180,
            allowed_extensions: '.pdf, .doc, .docx, .jpg, .jpeg, .png, .gif, .bmp, .tiff',
            max_contacts_per_node: 5,
          }}
        >
          <Row gutter={24}>
            <Col xs={24} lg={12}>
              <Form.Item label="上传大小上限（MB）" name="upload_size_mb" rules={[{ required: true, type: 'number', min: 0.01, message: '请输入大于 0 的大小上限' }]}>
                <InputNumber min={0.01} precision={2} style={{ width: '100%' }} />
              </Form.Item>
              <Form.Item label="文档页数上限" name="max_document_pages" rules={[{ required: true, type: 'number', min: 1, message: '请输入大于 0 的页数上限' }]}>
                <InputNumber min={1} precision={0} style={{ width: '100%' }} />
              </Form.Item>
              <Form.Item label="允许上传扩展名" name="allowed_extensions" rules={[{ required: true, message: '请输入允许上传的扩展名' }]}>
                <Input.TextArea rows={3} />
              </Form.Item>
            </Col>
            <Col xs={24} lg={12}>
              <Form.Item label="上传凭证有效期（秒）" name="upload_token_ttl_seconds" rules={[{ required: true, type: 'number', min: 1, message: '请输入大于 0 的上传凭证有效期' }]}>
                <InputNumber min={1} precision={0} style={{ width: '100%' }} />
              </Form.Item>
              <Form.Item label="下载凭证有效期（秒）" name="download_token_ttl_seconds" rules={[{ required: true, type: 'number', min: 1, message: '请输入大于 0 的下载凭证有效期' }]}>
                <InputNumber min={1} precision={0} style={{ width: '100%' }} />
              </Form.Item>
              <Form.Item label="每节点运维联系人上限" name="max_contacts_per_node" rules={[{ required: true, type: 'number', min: 1, max: 20, message: '请输入 1～20' }]}>
                <InputNumber min={1} max={20} precision={0} style={{ width: '100%' }} />
              </Form.Item>
            </Col>
          </Row>

          <Button type="primary" htmlType="submit" icon={<SaveOutlined />} loading={saving}>
            保存配置
          </Button>
        </Form>
      </Card>
    </div>
  );
};

export default BusinessSettings;
