package main

import (
	"time"
)

type NewPizzaOrderRequest struct {
	Size        string   `json:"size" validate:"required,oneof=small medium large"`
	Toppings    []string `json:"toppings" validate:"dive,required"`
	Destination string   `json:"destination" validate:"required"`
	Username    string   `json:"username" validate:"required"`
}

type NewPizzaOrderResponse struct {
	OrderID   string    `json:"order_id"`
	OrderedAt time.Time `json:"ordered_at"`
}

type Order struct {
	Size        string    `json:"size"`
	Toppings    []string  `json:"toppings"`
	Destination string    `json:"destination"`
	Username    string    `json:"username"`
	OrderedAt   time.Time `json:"ordered_at"`
	OrderID     string    `json:"order_id"`
	Status      string    `json:"status"` // e.g., "pending", "in_progress", "completed"
}
