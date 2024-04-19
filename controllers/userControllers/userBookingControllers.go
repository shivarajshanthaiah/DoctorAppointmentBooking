package userControllers

import (
	"doctorAppointment/configuration"
	"doctorAppointment/models"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// Function to GetAvailableTimeSlots
func GetAvailableTimeSlots(c *gin.Context) {
	doctorID := c.Param("doctor_id")
	dateStr := c.Query("date")

	// Parse date string into time.Time object
	date, err := time.Parse("2006-01-02", dateStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid date format"})
		return
	}

	// Query database for doctor's availability on the specified date
	var availability models.DoctorAvailability
	if err := configuration.DB.Where("doctor_id = ? AND date = ?", doctorID, date).First(&availability).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Availability not found"})
		return
	}

	// Split availability time into start and end time
	startTime, endTime := splitAvailabilityTime(availability.AvilableTime)

	// Divide time between start and end time into 30-minute intervals to create time slots
	availableTimeSlots := divideSlots(startTime, endTime, 30*time.Minute)

	// Query database for existing bookings for the doctor on the specified date
	var bookings []models.Appointment
	if err := configuration.DB.Where("doctor_id = ? AND appointment_date = ?", doctorID, date).Find(&bookings).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to retrieve bookings"})
		return
	}

	//Map to store booked time slots
	bookedTimeSlots := make(map[string]bool)
	for _, booking := range bookings {
		bookedTimeSlots[booking.AppointmentTimeSlot] = true
	}

	// Filter out available time slots that are already booked
	adjustedTimeSlots := make([]string, 0)
	for _, slot := range availableTimeSlots {
		if !bookedTimeSlots[slot] {
			adjustedTimeSlots = append(adjustedTimeSlots, slot)
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"date":                 dateStr,
		"available_time_slots": adjustedTimeSlots,
	})
}

// splits availability time string into start and end time
func splitAvailabilityTime(availabilityTime string) (startTime, endTime string) {
	parts := strings.Split(availabilityTime, "-")
	if len(parts) != 2 {
		return "", ""
	}
	// Trim any leading or trailing spaces from start and end times
	return strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
}

// Dividing time between start and end time into time slots with specified interval
func divideSlots(startTime, endTime string, interval time.Duration) []string {

	// Parse start and end time strings into time.Time objects
	start, _ := time.Parse("15:04", startTime)
	end, _ := time.Parse("15:04", endTime)

	// Initialize slice to store time slot strings
	var slots []string

	// Initialize slice to store time slot strings
	for t := start; t.Before(end); t = t.Add(interval) {
		slotEnd := t.Add(interval)
		slots = append(slots, fmt.Sprintf("%s-%s", t.Format("15:04"), slotEnd.Format("15:04")))
	}
	return slots
}

// Information about doctor
type DoctorInfo struct {
	Name       string `json:"name"`
	Age        int    `json:"age"`
	Gender     string `json:"gender" gorm:"not null"`
	Speciality string `json:"speciality"`
	Experience int    `json:"experience"`
	Location   string `json:"location"`
}

// Getting doctors by speciality
func GetDoctorsBySpeciality(c *gin.Context) {
	var doctors []models.Doctor
	doctorSpeciality := c.Param("specialization")

	// Query the database to find doctors with the specified speciality who are approved
	if err := configuration.DB.Where("specialization = ? AND approved = ?", doctorSpeciality, "true").Find(&doctors).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "No doctors found with the specified speciality"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{
			"Error":   "Couldn't Get doctors details",
			"details": err.Error()})
		return
	}

	// If the database query is successful, iterate over the list of doctors
	var doctorInfoList []DoctorInfo
	for _, doctor := range doctors {
		// Querying the database to find the hospital where the doctor works
		var hospital models.Hospital
		err := configuration.DB.Where("id = ?", doctor.HospitalID).First(&hospital).Error
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "Location error"})
			return
		}
		// Create an instance of DoctorInfo struct with doctor and hospital details
		doctorInfo := DoctorInfo{
			Name:       doctor.Name,
			Age:        doctor.Age,
			Gender:     doctor.Gender,
			Speciality: doctor.Specialization,
			Experience: doctor.Experience,
			Location:   hospital.Location,
		}
		doctorInfoList = append(doctorInfoList, doctorInfo)
	}

	if len(doctors) == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "No doctors found with the specified speciality"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"Status":  "Success",
		"Message": "Doctors details list fetched successfully",
		"data":    doctorInfoList,
	})
}

// Book appointment func
func BookAppointment(c *gin.Context) {
	var booking models.Appointment

	if err := c.BindJSON(&booking); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Check if the appointment time slot is within the available time slots of the doctor
	doctorAvailability := getDoctorAvailability(booking.DoctorID, booking.AppointmentDate)
	if doctorAvailability == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Doctor availability not found"})
		return
	}

	// Divide available time slots into smaller slots
	availableTimeSlots := divideAvailableSlots(doctorAvailability.AvilableTime, 30*time.Minute)

	// Check if the appointment time slot is within the available time slots
	if !isTimeWithinAvailableSlot(booking.AppointmentTimeSlot, availableTimeSlots) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Appointment time slot not available"})
		return
	}

	// Check for existing appointments with the same date and time slot
	if !isAppointmentAvailable(booking.DoctorID, booking.AppointmentDate, booking.AppointmentTimeSlot) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Appointment has been already booked for the same date and time slot with the doctor"})
		return
	}

	// Check if the patient exists
	var patient models.Patient
	if err := configuration.DB.Where("patient_id = ?", booking.PatientID).First(&patient).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Wrong patient ID"})
		return
	}

	// Check for duplicate appointments with the same doctor on the same day
	if !isDuplicateAppointment(booking.PatientID, booking.DoctorID, booking.AppointmentDate) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Your Appointment has been already booked with the same doctor in the same day"})
		return
	}
	// Create the appointment
	booking.BookingStatus = "pending"
	booking.PaymentStatus = "pending"
	if err := configuration.DB.Create(&booking).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to book appointment"})
		return
	}

	// Fetch doctor's consultancy charge
	var doctor models.Doctor
	if err := configuration.DB.Where("doctor_id = ?", booking.DoctorID).First(&doctor).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch doctor's consultancy charge"})
		return
	}

	// Calculate total amount for the invoice
	totalAmount := doctor.ConsultancyCharge

	// Create the invoice
	invoice := models.Invoice{
		DoctorID:       uint(booking.DoctorID),
		PatientID:      uint(booking.PatientID),
		AppointmentID:  uint(booking.AppointmentID),
		TotalAmount:    float64(totalAmount) + 50,
		PaymentMethod:  "Pending", // Payment method set to pending initially
		PaymentStatus:  "Pending",
		PaymentDueDate: time.Now().AddDate(0, 0, 1), // Payment due date set to 1 day from now
	}

	if err := configuration.DB.Create(&invoice).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create invoice"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"Status":  "Success",
		"Message": "Appointment booked successfully",
		"Data":    booking,
		"Invoice": invoice,
	})
}

// getDoctorAvailability retrieves the availability of a doctor on a specific date
func getDoctorAvailability(doctorID int, date time.Time) *models.DoctorAvailability {
	var availability models.DoctorAvailability
	if err := configuration.DB.Where("doctor_id = ? AND date = ?", doctorID, date).First(&availability).Error; err != nil {
		return nil
	}
	return &availability
}

// isTimeWithinAvailableSlot checks if the appointment time slot falls within the available time slots
func isTimeWithinAvailableSlot(appointmentTimeSlot string, availableSlots []string) bool {
	for _, slot := range availableSlots {
		if slot == appointmentTimeSlot {
			return true
		}
	}
	return false
}

// isAppointmentAvailable checks if there is already an appointment booked with the same doctor, date, and time slot
func isAppointmentAvailable(doctorID int, date time.Time, appointmentTimeSlot string) bool {
	var existingAppointment models.Appointment
	err := configuration.DB.Where("doctor_id = ? AND appointment_date = ? AND appointment_time_slot = ? AND payment_status = ?", doctorID, date, appointmentTimeSlot, "paid").First(&existingAppointment).Error
	if err == nil {
		return false // Appointment already exists for the same date and time slot
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		// An unexpected error occurred while querying the database
		log.Println("Error checking for existing appointment:", err)
		return false
	}
	return true
}

// divideAvailableSlots divides the available time slots of a doctor into smaller time slots based on the specified interval
func divideAvailableSlots(availability string, interval time.Duration) []string {
	// Extract start and end times from the availability string
	parts := strings.Split(availability, "-")
	if len(parts) != 2 {
		fmt.Println("Invalid availability format")
		return nil
	}
	startTime := parts[0]
	endTime := parts[1]

	// Parse start and end times
	start, _ := time.Parse("15:04", startTime)
	end, _ := time.Parse("15:04", endTime)

	var slots []string
	for t := start; t.Before(end); t = t.Add(interval) {
		slotEnd := t.Add(interval)
		slots = append(slots, fmt.Sprintf("%s-%s", t.Format("15:04"), slotEnd.Format("15:04")))
	}
	return slots
}

// isDuplicateAppointment checks if there are any duplicate appointments with the same doctor on the same day
func isDuplicateAppointment(patientID int, doctorID int, date time.Time) bool {
	var existingAppointments []models.Appointment
	err := configuration.DB.Where("patient_id = ? AND doctor_id = ? AND appointment_date = ? AND payment_status =?", patientID, doctorID, date, "paid").Find(&existingAppointments).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return true // No existing appointments found for the same doctor and date
		}
		// An unexpected error occurred while querying the database
		log.Println("Error checking for existing appointments:", err)
		return false
	}
	// Found existing appointments for the same doctor and date
	return len(existingAppointments) == 0
}