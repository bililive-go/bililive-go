import React, { useState, useMemo } from 'react';
import { Select, Button, Space, Alert } from 'antd';
import { StreamAttributes } from '../../types/stream';

interface AttributeSelectorProps {
  attributeCombinations: StreamAttributes[]; // 从 API 获取的可用属性组合
  onSelect: (attributes: StreamAttributes) => void; // 应用选择时的回调
  isRecording: boolean; // 是否正在录制
}

/**
 * 流属性选择器组件
 * 根据可用的流属性组合，动态生成下拉菜单，并实现属性间的联动过滤
 */
const AttributeSelector: React.FC<AttributeSelectorProps> = ({
  attributeCombinations,
  onSelect,
  isRecording,
}) => {
  const [selectedAttrs, setSelectedAttrs] = useState<StreamAttributes>({});

  // 提取所有属性的 key（如：画质、format、codec 等）
  const allKeys = useMemo(() => {
    const keysSet = new Set<string>();
    attributeCombinations.forEach((combo) => {
      Object.keys(combo).forEach((key) => keysSet.add(key));
    });
    return Array.from(keysSet);
  }, [attributeCombinations]);

  /**
   * 根据当前已选属性，计算指定属性 key 的有效值
   * @param key 要计算的属性 key
   * @returns 该属性的所有有效值数组
   */
  const getValidValues = (key: string): string[] => {
    // 过滤出与当前已选属性兼容的组合
    const compatible = attributeCombinations.filter((combo) => {
      // 检查是否与当前已选属性兼容
      return Object.entries(selectedAttrs).every(([k, v]) => {
        if (k === key) return true; // 自己可以任意
        return combo[k] === undefined || combo[k] === v;
      });
    });

    // 提取该属性的所有可能值
    const values = new Set<string>();
    compatible.forEach((combo) => {
      if (combo[key]) values.add(combo[key]);
    });

    return Array.from(values);
  };

  /**
   * 处理属性值变化
   * @param key 属性 key
   * @param value 新值（undefined 表示清空）
   */
  const handleChange = (key: string, value: string | undefined) => {
    setSelectedAttrs((prev) => {
      const newAttrs = { ...prev };
      if (value === undefined) {
        delete newAttrs[key];
      } else {
        newAttrs[key] = value;
      }
      return newAttrs;
    });
  };

  /**
   * 应用当前选择
   */
  const handleApply = () => {
    onSelect(selectedAttrs);
  };

  /**
   * 计算当前选择匹配的流数量
   */
  const matchedCount = useMemo(() => {
    if (Object.keys(selectedAttrs).length === 0) {
      return attributeCombinations.length;
    }

    return attributeCombinations.filter((combo) => {
      return Object.entries(selectedAttrs).every(([k, v]) => {
        return combo[k] === v;
      });
    }).length;
  }, [selectedAttrs, attributeCombinations]);

  // 如果没有可用的属性组合，显示提示信息
  if (attributeCombinations.length === 0) {
    return (
      <Alert
        type="info"
        message="暂无可用流信息"
        description="当直播间开播后，将显示可用的流属性选择器。"
      />
    );
  }

  return (
    <Space direction="vertical" style={{ width: '100%' }}>
      {/* 动态生成的下拉菜单 */}
      {allKeys.map((key) => {
        const validValues = getValidValues(key);
        return (
          <Space key={key} style={{ width: '100%', justifyContent: 'space-between' }}>
            <label style={{ minWidth: '80px' }}>{key}:</label>
            <Select
              value={selectedAttrs[key]}
              onChange={(v) => handleChange(key, v)}
              placeholder="不限制"
              allowClear
              style={{ flex: 1, minWidth: '150px' }}
            >
              {validValues.map((v) => (
                <Select.Option key={v} value={v}>
                  {v}
                </Select.Option>
              ))}
            </Select>
            <span style={{ color: '#999', fontSize: '12px', minWidth: '80px' }}>
              ({validValues.length} 个选项)
            </span>
          </Space>
        );
      })}

      {/* 匹配流数量提示 */}
      <div style={{ color: '#1890ff', fontSize: '14px', marginTop: '8px' }}>
        当前选择匹配 {matchedCount} 个可用流
      </div>

      {/* 录制中警告 */}
      {isRecording && (
        <Alert
          type="warning"
          message="注意：切换流将中断当前录制"
          description="应用新的流设置后，系统将停止当前录制并保存文件，然后使用新的流设置重新开始录制。"
          showIcon
        />
      )}

      {/* 应用按钮 */}
      <Button type="primary" onClick={handleApply} block>
        应用选择
      </Button>
    </Space>
  );
};

export default AttributeSelector;
